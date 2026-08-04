package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"deploy/internal/build"
	"deploy/internal/audit"
	"deploy/internal/caddyfile"
	"deploy/internal/config"
	"deploy/internal/runner"
	"deploy/internal/state"
	"deploy/internal/types"
	"github.com/google/uuid"
	moby "github.com/moby/moby/client"
)


// ProgressFunc is a callback for streaming progress during long operations.
type ProgressFunc func(event types.ProgressEvent)

// dockerClient abstracts the Docker SDK operations the deployer needs.
type dockerClient interface {
	ImageBuild(ctx context.Context, buildContext io.Reader, opts moby.ImageBuildOptions) (moby.ImageBuildResult, error)
	ImageSave(ctx context.Context, images []string, opts ...moby.ImageSaveOption) (moby.ImageSaveResult, error)
	ImageLoad(ctx context.Context, input io.Reader, opts ...moby.ImageLoadOption) (moby.ImageLoadResult, error)
	ImageInspect(ctx context.Context, imageID string, opts ...moby.ImageInspectOption) (moby.ImageInspectResult, error)
}

// HealthCheckFunc is a function that performs a health check.
// Returns nil if healthy, error otherwise.
type HealthCheckFunc func(ctx context.Context, port int, path string, initialDelay, interval, timeout time.Duration, retries int) error

// Deployer orchestrates the full deploy lifecycle: build, start, health check,
// and cutover. The Promote method is the single atomic deploy command.
type Deployer struct {
	runner         runner.Interface
	client         dockerClient
	db             *sql.DB
	lockManager    *LockManager
	masterKey      []byte
	healthCheckFn  HealthCheckFunc
	caddyManager   *caddyfile.CaddyManager
}

// NewDeployer creates a new Deployer.
func NewDeployer(runner runner.Interface, client dockerClient, db *sql.DB, lockManager *LockManager, masterKey []byte) *Deployer {
	return &Deployer{
		runner:        runner,
		client:        client,
		db:            db,
		lockManager:   lockManager,
		masterKey:     masterKey,
		healthCheckFn: defaultHealthCheck,
	}
}

// SetHealthCheckFunc overrides the default health check function (for testing).
func (d *Deployer) SetHealthCheckFunc(fn HealthCheckFunc) {
	d.healthCheckFn = fn
}

// SetCaddyManager sets the Caddy manager for port update notifications.
func (d *Deployer) SetCaddyManager(cm *caddyfile.CaddyManager) {
	d.caddyManager = cm
}

// defaultHealthCheck polls the HTTP endpoint of the new container until it
// responds successfully or the retry limit is reached.
func defaultHealthCheck(ctx context.Context, port int, path string,
	initialDelay, interval, timeout time.Duration, retries int) error {

	select {
	case <-time.After(initialDelay):
	case <-ctx.Done():
		return ctx.Err()
	}

	httpClient := &http.Client{Timeout: timeout}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)

	for i := 0; i < retries; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		resp, err := httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			}
		}

		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("health check failed after %d retries", retries)
}

// Promote builds, deploys, health-checks, and cuts over traffic to a new container.
func (d *Deployer) Promote(ctx context.Context, req *types.PromoteRequest, appName, dir string, progress ProgressFunc) (*types.PromoteResponse, error) {
	if progress == nil {
		progress = func(types.ProgressEvent) {}
	}
	lock, err := d.lockManager.Acquire(appName)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	startTime := time.Now()
	var auditVersion string

	cfg, err := config.LoadDeployConfig(filepath.Join(dir, "deploy.yml"))
	if err != nil {
		return nil, fmt.Errorf("parse deploy.yml: %w", err)
	}
	progress(types.ProgressEvent{Step: "config", Message: "Loaded deploy config", Status: "done"})

	app, err := state.GetAppByName(d.db, appName)
	if err != nil {
		return nil, fmt.Errorf("get app: %w", err)
	}
	if app == nil {
		// Auto-create app from deploy.yml
		var hostPort int
		if len(cfg.Ports) > 0 && cfg.Ports[0].Host > 0 {
			hostPort = cfg.Ports[0].Host
		} else {
			// Auto-assign from pool on first deploy
			assigned, err := state.AllocatePort(d.db, appName, nil)
			if err != nil {
				return nil, fmt.Errorf("auto-assigning port: %w", err)
			}
			hostPort = assigned
		}
		newApp := &types.App{
			ID:     uuid.New().String(),
			Name:   appName,
			Status: types.StatusCreated,
			Port:   hostPort,
			Image:  appName + ":latest",
			Env:    cfg.Env,
			Network: cfg.Network,
		}
		if cfg.Resources.Memory != "" || cfg.Resources.CPUs != "" {
			r := cfg.Resources
			newApp.Resources = &r
		}
		if _, createErr := state.CreateApp(d.db, newApp); createErr != nil {
			return nil, fmt.Errorf("create app %q: %w", appName, createErr)
		}
		app = newApp
		log.Printf("auto-created app %q from deploy.yml (port=%d)", appName, hostPort)
	}

	// 1. Build the image
	buildID := uuid.New().String()
	shortID := buildID[:8]
	version := fmt.Sprintf("v%s-%s", time.Now().UTC().Format("20060102150405"), shortID)
 	auditVersion = version
	imageRef := fmt.Sprintf("%s:%s", appName, version)

	progress(types.ProgressEvent{Step: "build", Message: "Building Docker image...", Status: "running"})
	dockerfilePath := filepath.Join(dir, cfg.Build.Dockerfile)
	buildContextDir := filepath.Join(dir, cfg.Build.Context)

	log.Printf("building %s (dockerfile=%s, context=%s)", imageRef, dockerfilePath, buildContextDir)

	builder := build.NewBuilder(d.client)
	buildCfg := build.BuildConfig{
		ImageRef:   imageRef,
		ContextDir: buildContextDir,
		Dockerfile: cfg.Build.Dockerfile,
		BuildArgs:  cfg.Build.Args,
		Target:     cfg.Build.Target,
	}
	buildResult, err := builder.BuildFromConfig(ctx, buildCfg)
	if err != nil {
		return nil, fmt.Errorf("build image: %w", err)
	}
	progress(types.ProgressEvent{Step: "build", Message: "Build complete", Status: "done"})
	digest := buildResult.ImageDigest


	// Record old container ID before making any changes
	// (used for rollback in case new container fails health check)
	oldContainerID := ""
	oldPort := 0
	if app.ContainerID != "" {
		oldContainerID = app.ContainerID
		oldPort = app.Port
	}

	// 4. Health check old container before proceeding
	if oldContainerID != "" {
		healthPath := "/"
		if cfg.Health.Path != "" {
			healthPath = cfg.Health.Path
		}
		// Use runner.HealthCheck which checks Docker state + HTTP
		if err := d.runner.HealthCheck(ctx, oldContainerID, oldPort, healthPath, 0, 500*time.Millisecond, 2*time.Second, 3); err != nil {
			log.Printf("warning: old container health check failed: %v (proceeding anyway)", err)
		}
	}

	// 5. Create deployment record
	depID := uuid.New().String()

	dep := &types.Deployment{
		ID:          depID,
		AppID:       app.ID,
		Version:     buildResult.Version,
		ImageDigest: digest,
		Status:      types.DeployStatusDeploying,
		// Stable port: the deployment reuses the app's current port. The old
		// container is stopped before the new one is created (see below), so
		// the port is actually free and never drifts across deploy cycles.
		Port:        app.Port,
	}
	if _, err := state.CreateDeployment(d.db, dep); err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}

	// 6. Get secrets from DB and merge env
	secrets, err := state.ListSecretsByApp(d.db, app.ID, d.masterKey)
	if err != nil {
		secrets = nil
	}
	// Get group env if app belongs to a group
	var groupEnv map[string]string
	if app.GroupID != nil {
		groupEnv, _ = state.GetGroupEnv(d.db, *app.GroupID)
	}
	// Merge deploy.yml env into app env (deploy.yml overrides DB)
	mergedAppEnv := make(map[string]string)
	for k, v := range app.Env {
		mergedAppEnv[k] = v
	}
	for k, v := range cfg.Env {
		mergedAppEnv[k] = v
	}
	mergedEnv := state.MergeEnv(mergedAppEnv, groupEnv, secrets)

	// 7. Stop the old container FIRST so its port is freed and the new
	// container can bind the app's stable port. It is only stopped (not
	// removed) so a failed health check can bring it back by restarting it.
	if oldContainerID != "" {
		progress(types.ProgressEvent{Step: "stop", Message: "Stopping old container...", Status: "running"})
		if err := d.runner.StopContainer(ctx, oldContainerID); err != nil {
			state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed,
				fmt.Sprintf("stop old container: %v", err))
			return nil, fmt.Errorf("stop old container: %w", err)
		}
		progress(types.ProgressEvent{Step: "stop", Message: "Old container stopped", Status: "done"})
	}

	// 8. Start new container on the app's stable port (reused across deploys).
	hostPort := app.Port
	svcPort := hostPort
	if len(cfg.Ports) > 0 {
		svcPort = cfg.Ports[0].Container
	}

	progress(types.ProgressEvent{Step: "start", Message: "Starting new container...", Status: "running"})
	containerID, err := d.createPromoteContainer(ctx, app, buildResult.ImageRef, mergedEnv, hostPort, svcPort, version, cfg.Resources, cfg.Network)
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
	progress(types.ProgressEvent{Step: "start", Message: "Container started", Status: "done"})

	// 9. Health check new container
	healthPath := "/"
	if cfg.Health.Path != "" {
		healthPath = cfg.Health.Path
	}
	state.UpdateAppHealthPath(d.db, appName, healthPath)
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

	progress(types.ProgressEvent{Step: "health", Message: "Waiting for health check...", Status: "running"})
	log.Printf("health checking new container %s on port %d...", containerID[:12], hostPort)
	if err := d.runner.HealthCheck(ctx, containerID, hostPort, healthPath, initialDelay, interval, timeout, retries); err != nil {
		d.runner.StopContainer(ctx, containerID)
		if err := d.runner.RemoveContainer(ctx, containerID); err != nil {
			log.Printf("warning: remove container %s: %v", containerID[:12], err)
		}
		state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed,
			fmt.Sprintf("health check: %v", err))

		// Rollback: restart old container on its original port if it exists
		if oldContainerID != "" {
			log.Printf("promote failed, restarting old container %s on port %d...", oldContainerID[:12], oldPort)
			// Check old container state — it should still be running since we
			// haven't stopped it, but be defensive
			if cs, inspectErr := d.runner.InspectContainer(ctx, oldContainerID); inspectErr == nil && !cs.Running {
				if startErr := d.runner.StartContainer(ctx, oldContainerID); startErr != nil {
					log.Printf("warning: restart old container %s: %v", oldContainerID[:12], startErr)
				}
			}
		}

		audit.Log(audit.Entry{
			Time:        time.Now().UTC(),
			Action:      "promote",
			App:         appName,
			Version:     auditVersion,
			DurationMs:  time.Since(startTime).Milliseconds(),
			Result:      fmt.Sprintf("health check: %v", err),
			InitiatedBy: audit.CurrentUser(),
		})
		return nil, fmt.Errorf("health check: %w", err)
	}
	progress(types.ProgressEvent{Step: "health", Message: "Health check passed", Status: "done"})

	// 10. Remove the old container (already stopped above; keep it around until
	// the new container passed its health check so a failure can restart it).
	if oldContainerID != "" {
		progress(types.ProgressEvent{Step: "cleanup", Message: "Removing old container...", Status: "running"})
		if err := d.runner.RemoveContainer(ctx, oldContainerID); err != nil {
			log.Printf("warning: remove old container %s: %v", oldContainerID[:12], err)
		}
		progress(types.ProgressEvent{Step: "cleanup", Message: "Old container removed", Status: "done"})
	}

	// 11. Update app record with new container ID (atomic transaction)
	// (port stays the same)
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
	if err := state.UpdateAppResources(tx, appName, &cfg.Resources); err != nil {
		return nil, fmt.Errorf("update app resources: %w", err)
	}
	if err := state.UpdateAppStatus(tx, appName, types.StatusRunning); err != nil {
		return nil, fmt.Errorf("update app status: %w", err)
	}
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

	// 12. Update port allocation
	if err := state.UpdatePortAllocation(d.db, appName, hostPort); err != nil {
		log.Printf("warning: update port allocation: %v", err)
	}

	// 13. Conf from truth: regenerate every site snippet from the app's actual
	// port/status in the DB (just committed above), never from the old or the
	// staged port. Then register any deploy.yml domains not already present.
	if d.caddyManager != nil && d.caddyManager.IsRunning() {
		progress(types.ProgressEvent{Step: "caddy", Message: "Regenerating Caddy config from state...", Status: "running"})
		if err := d.caddyManager.Reload(); err != nil {
			return nil, fmt.Errorf("caddy reload from state: %w", err)
		}
		progress(types.ProgressEvent{Step: "caddy", Message: "Caddy config regenerated", Status: "done"})
		for _, domain := range cfg.Domains {
			// Domains from deploy.yml use the cert-based path (httpOnly=false).
			if err := d.caddyManager.AddDomainSnippet(appName, domain, hostPort, false); err != nil {
				log.Printf("warning: failed to register domain %s: %v", domain, err)
			}
		}
	}

	log.Printf("promoted %s -> %s (container=%s, port=%d)", appName, version, containerID[:12], hostPort)
	// Clean up old tarballs (non-fatal)
	if err := build.CleanOldTarballs(appName, 5); err != nil {
		log.Printf("warning: clean old tarballs: %v", err)
	}
	audit.Log(audit.Entry{
		Time:        time.Now().UTC(),
		Action:      "promote",
		App:         appName,
		Version:     version,
		DurationMs:  time.Since(startTime).Milliseconds(),
		Result:      "ok",
		InitiatedBy: audit.CurrentUser(),
	})
	return &types.PromoteResponse{
		Message:        fmt.Sprintf("deployed %s to version %s in %.0fs", appName, version, time.Since(dep.CreatedAt).Seconds()),
		Version:        version,
		NewContainerID: containerID,
		Port:           hostPort,
		OldContainerID: oldContainerID,
	}, nil
}

// parseDockerBuildResponse reads a Docker build JSON stream and returns
// the build output. If the build fails, it returns a BuildError with detail.

// buildResult holds the result of a successful image build.

// createPromoteContainer sets up the container config for a new deployment.
func (d *Deployer) createPromoteContainer(ctx context.Context, app *types.App, imageRef string, env []string, hostPort, svcPort int, version string, resources types.ResourceConfig, network string) (string, error) {
	// Convert []string env (KEY=VALUE) to map[string]string for the app object
	envMap := make(map[string]string, len(env))
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			envMap[k] = v
		}
	}

	promoteApp := &types.App{
		ID:    app.ID,
		Name:  app.Name,
		Port:        hostPort,
		ServicePort: svcPort,
		Image: imageRef,
		Env:   envMap,
		Network: network,
	}
	if resources.Memory != "" || resources.CPUs != "" {
		r := resources
		promoteApp.Resources = &r
	}
	return d.runner.CreateContainer(ctx, promoteApp, version)
}