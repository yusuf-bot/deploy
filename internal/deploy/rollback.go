package deploy

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"time"

	"deploy/internal/build"
	"deploy/internal/config"
	"deploy/internal/state"
	"deploy/internal/types"
	"deploy/internal/audit"

	"github.com/google/uuid"
	moby "github.com/moby/moby/client"
)

// Rollback reverts an app to a previous version by loading its saved tarball,
// creating a new container, health checking, and cutting over traffic.
func (d *Deployer) Rollback(ctx context.Context, appName, targetVersion, dir string) (*types.PromoteResponse, error) {
	lock, err := d.lockManager.Acquire(appName)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	startTime := time.Now()
	var auditVersion string

	app, err := state.GetAppByName(d.db, appName)
	if err != nil {
		return nil, fmt.Errorf("get app: %w", err)
	}
	if app == nil {
		return nil, fmt.Errorf("app %q not found", appName)
	}

	// Parse deploy.yml for health check config
	cfg, err := config.LoadDeployConfig(filepath.Join(dir, "deploy.yml"))
	if err != nil {
		return nil, fmt.Errorf("parse deploy.yml: %w", err)
	}

	// Determine the version to roll back to
	if targetVersion == "" {
		// List all deployments for this app, newest first, find previous active
		all, err := state.ListDeploymentsByApp(d.db, app.ID)
		if err != nil {
			return nil, fmt.Errorf("list deployments: %w", err)
		}
		var prev *types.Deployment
		for i, dep := range all {
			if dep.Status == types.DeployStatusActive && i+1 < len(all) {
				prev = &all[i+1]
				break
			}
		}
		if prev == nil {
			return nil, fmt.Errorf("no previous deployment found for %q", appName)
		}
		targetVersion = prev.Version
	}
 	auditVersion = targetVersion

	// Load the image tarball (zstd or legacy format); errors if neither exists
	tarball, err := build.OpenTarball(appName, targetVersion)
	if err != nil {
		return nil, err
	}
	defer tarball.Close()

	// Record old container info before making any changes
	oldContainerID := ""
	oldPort := 0
	if app.ContainerID != "" {
		oldContainerID = app.ContainerID
		oldPort = app.Port
	}

	// Load the image from tarball
	imageRef := fmt.Sprintf("%s:%s", appName, targetVersion)

	loadResult, err := d.client.ImageLoad(ctx, tarball, moby.ImageLoadWithQuiet(true))
	if err != nil {
		return nil, fmt.Errorf("load image: %w", err)
	}
	defer loadResult.Close()
	// Drain the response
	io.Copy(io.Discard, loadResult)

	// Create deployment record
	depID := uuid.New().String()

	dep := &types.Deployment{
		ID:      depID,
		AppID:   app.ID,
		Version: targetVersion,
		Status:  types.DeployStatusDeploying,
		// Stable port: rollback reuses the app's current port. The old container
		// is stopped before the new one is created (see below), so the port is
		// actually free and never drifts across rollback cycles.
		Port:    app.Port,
	}
	if _, err := state.CreateDeployment(d.db, dep); err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}

	// Get secrets and merge env
	secrets, err := state.ListSecretsByApp(d.db, app.ID, d.masterKey)
	if err != nil {
		secrets = nil
	}
	mergedEnv := state.MergeEnv(app.Env, secrets)

	// Stop the old container FIRST so its port is freed and the new container
	// can bind the app's stable port. It is only stopped (not removed) so a
	// failed health check can bring it back by restarting it.
	if oldContainerID != "" {
		if err := d.runner.StopContainer(ctx, oldContainerID); err != nil {
			state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed,
				fmt.Sprintf("stop old container: %v", err))
			return nil, fmt.Errorf("stop old container: %w", err)
		}
	}

	// Create new container on the app's stable port (reused across rollbacks).
	hostPort := app.Port
	svcPort := hostPort
	if len(cfg.Ports) > 0 {
		svcPort = cfg.Ports[0].Container
	}

	containerID, err := d.createPromoteContainer(ctx, app, imageRef, mergedEnv, hostPort, svcPort, targetVersion, cfg.Resources, cfg.Network)
	if err != nil {
		state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed,
			fmt.Sprintf("create container: %v", err))
		return nil, fmt.Errorf("create container: %w", err)
	}

	// Start the container
	if err := d.runner.StartContainer(ctx, containerID); err != nil {
		if err := d.runner.RemoveContainer(ctx, containerID); err != nil {
			log.Printf("warning: remove container %s: %v", containerID[:12], err)
		}
		state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed,
			fmt.Sprintf("start container: %v", err))
		return nil, fmt.Errorf("start container: %w", err)
	}

	// Health check
	healthPath := "/"
	if cfg.Health.Path != "" {
		healthPath = cfg.Health.Path
	}
	initialDelay := 1 * time.Second
	if cfg.Health.InitialDelay != "" {
		if d, err := time.ParseDuration(cfg.Health.InitialDelay); err == nil {
			initialDelay = d
		}
	}
	interval := 2 * time.Second
	if cfg.Health.Interval != "" {
		if d, err := time.ParseDuration(cfg.Health.Interval); err == nil {
			interval = d
		}
	}
	timeout := 5 * time.Second
	if cfg.Health.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Health.Timeout); err == nil {
			timeout = d
		}
	}
	retries := 15
	if cfg.Health.Retries > 0 {
		retries = cfg.Health.Retries
	}

	log.Printf("health checking rollback container %s on port %d...", containerID[:12], hostPort)
	if err := d.runner.HealthCheck(ctx, containerID, hostPort, healthPath, initialDelay, interval, timeout, retries); err != nil {
		d.runner.StopContainer(ctx, containerID)
		if err := d.runner.RemoveContainer(ctx, containerID); err != nil {
			log.Printf("warning: remove container %s: %v", containerID[:12], err)
		}
		state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed,
			fmt.Sprintf("health check: %v", err))

		// Rollback: restart old container on its original port if it exists
		if oldContainerID != "" {
			log.Printf("rollback failed, restarting old container %s on port %d...", oldContainerID[:12], oldPort)
			if cs, inspectErr := d.runner.InspectContainer(ctx, oldContainerID); inspectErr == nil && !cs.Running {
				if startErr := d.runner.StartContainer(ctx, oldContainerID); startErr != nil {
					log.Printf("warning: restart old container %s: %v", oldContainerID[:12], startErr)
				}
			}
		}

		audit.Log(audit.Entry{
			Time:       time.Now().UTC(),
			Action:     "rollback",
			App:        appName,
			Version:    auditVersion,
			DurationMs: time.Since(startTime).Milliseconds(),
			Result:     fmt.Sprintf("health check: %v", err),
			InitiatedBy: audit.CurrentUser(),
		})
		return nil, fmt.Errorf("health check: %w", err)
	}

	// Remove the old container (already stopped above; keep it around until the
	// new container passed its health check so a failure can restart it).
	if oldContainerID != "" {
		if err := d.runner.RemoveContainer(ctx, oldContainerID); err != nil {
			log.Printf("warning: remove old container %s: %v", oldContainerID[:12], err)
		}
	}

	// Update app record and mark deployment active (atomic transaction)
	tx, err := d.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := state.UpdateAppContainer(tx, appName, containerID); err != nil {
		return nil, fmt.Errorf("update app container: %w", err)
	}
	if err := state.UpdateAppPort(tx, appName, hostPort); err != nil {
		return nil, fmt.Errorf("update app port: %w", err)
	}
	if err := state.UpdateAppServicePort(tx, appName, svcPort); err != nil {
		return nil, fmt.Errorf("update app service port: %w", err)
	}
	if err := state.UpdateAppStatus(tx, appName, types.StatusRunning); err != nil {
		return nil, fmt.Errorf("update app status: %w", err)
	}
	dep.CreatedAt = time.Now().UTC()
	if err := state.UpdateDeploymentStatus(tx, depID, types.DeployStatusActive, ""); err != nil {
		return nil, fmt.Errorf("update deployment status: %w", err)
	}
	if err := state.SetActiveDeployment(tx, dep); err != nil {
		log.Printf("warning: set active deployment: %v", err)
	}
	if err := state.DeactivateOtherDeployments(tx, app.ID, depID); err != nil {
		log.Printf("warning: deactivate other deployments: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	// Update port allocation
	if err := state.UpdatePortAllocation(d.db, appName, hostPort); err != nil {
		log.Printf("warning: update port allocation: %v", err)
	}

	// Conf from truth: regenerate every site snippet from the app's actual
	// port/status in the DB (just committed above), never from the old port.
	if d.caddyManager != nil && d.caddyManager.IsRunning() {
		if err := d.caddyManager.Reload(); err != nil {
			return nil, fmt.Errorf("caddy reload from state: %w", err)
		}
	}

	log.Printf("rolled back %s to %s (container=%s, port=%d)", appName, targetVersion, containerID[:12], hostPort)
	audit.Log(audit.Entry{
		Time:       time.Now().UTC(),
		Action:     "rollback",
		App:        appName,
		Version:    targetVersion,
		DurationMs: time.Since(startTime).Milliseconds(),
		Result:     "ok",
			InitiatedBy: audit.CurrentUser(),
	})
	// Clean up old tarballs (non-fatal)
	if err := build.CleanOldTarballs(appName, 5); err != nil {
		log.Printf("warning: clean old tarballs: %v", err)
	}
	return &types.PromoteResponse{
		Message:       fmt.Sprintf("rolled back %s to %s", appName, targetVersion),
		Version:       targetVersion,
		NewContainerID: containerID,
		Port:          hostPort,
		OldContainerID: oldContainerID,
	}, nil
}