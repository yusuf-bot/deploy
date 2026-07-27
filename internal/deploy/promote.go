package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
	url := fmt.Sprintf("http://localhost:%d%s", port, path)

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
func (d *Deployer) Promote(ctx context.Context, req *types.PromoteRequest, appName, dir string) (*types.PromoteResponse, error) {
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

	// Parse deploy.yml
	cfg, err := config.LoadDeployConfig(filepath.Join(dir, "deploy.yml"))
	if err != nil {
		return nil, fmt.Errorf("parse deploy.yml: %w", err)
	}

	// 1. Build the image
	buildID := uuid.New().String()
	shortID := buildID[:8]
	version := fmt.Sprintf("v%s-%s", time.Now().UTC().Format("20060102150405"), shortID)
 	auditVersion = version
	imageRef := fmt.Sprintf("%s:%s", appName, version)

	dockerfilePath := filepath.Join(dir, cfg.Build.Dockerfile)
	buildContextDir := filepath.Join(dir, cfg.Build.Context)

	log.Printf("building %s (dockerfile=%s, context=%s)", imageRef, dockerfilePath, buildContextDir)

	buildResult, err := d.buildImage(ctx, buildContextDir, dockerfilePath, imageRef)
	if err != nil {
		return nil, fmt.Errorf("build image: %w", err)
	}

	// 2. Save image to tarball for rollback archive
	tarballPath := build.TarballPath(appName, version)
	if err := os.MkdirAll(filepath.Dir(tarballPath), 0700); err != nil {
		return nil, fmt.Errorf("create tarball dir: %w", err)
	}

	saveResult, err := d.client.ImageSave(ctx, []string{imageRef})
	if err != nil {
		return nil, fmt.Errorf("save image: %w", err)
	}
	defer saveResult.Close()

	tarball, err := os.Create(tarballPath)
	if err != nil {
		return nil, fmt.Errorf("create tarball file: %w", err)
	}
	defer tarball.Close()

	if _, err := io.Copy(tarball, saveResult); err != nil {
		return nil, fmt.Errorf("write tarball: %w", err)
	}

	// 3. Inspect the built image for digest
	imgInspect, err := d.client.ImageInspect(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("inspect image: %w", err)
	}
	digest := imgInspect.ID

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
		Port:        app.Port + 1,
	}
	if _, err := state.CreateDeployment(d.db, dep); err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}

	// 6. Get secrets from DB and merge env
	secrets, err := state.ListSecretsByApp(d.db, app.ID, d.masterKey)
	if err != nil {
		secrets = nil
	}
	mergedEnv := mergeEnv(app.Env, secrets)

	// 7. Start new container on appPort+1
	hostPort := app.Port + 1
	svcPort := hostPort
	if len(cfg.Ports) > 0 {
		svcPort = cfg.Ports[0].Container
	}

	containerID, err := d.createPromoteContainer(ctx, app, buildResult.ImageRef, mergedEnv, hostPort, svcPort, version)
	if err != nil {
		state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed,
			fmt.Sprintf("create container: %v", err))
		return nil, fmt.Errorf("create container: %w", err)
	}

	// Start the container
	if err := d.runner.StartContainer(ctx, containerID); err != nil {
		d.runner.RemoveContainer(ctx, containerID)
		state.UpdateDeploymentStatus(d.db, depID, types.DeployStatusFailed,
			fmt.Sprintf("start container: %v", err))
		return nil, fmt.Errorf("start container: %w", err)
	}

	// 8. Health check new container
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

	log.Printf("health checking new container %s on port %d...", containerID[:12], hostPort)
	if err := d.runner.HealthCheck(ctx, containerID, hostPort, healthPath, initialDelay, interval, timeout, retries); err != nil {
		d.runner.StopContainer(ctx, containerID)
		d.runner.RemoveContainer(ctx, containerID)
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
			Time:       time.Now().UTC(),
			Action:     "promote",
			App:        appName,
			Version:    auditVersion,
			DurationMs: time.Since(startTime).Milliseconds(),
			Result:     fmt.Sprintf("health check: %v", err),
		})
		return nil, fmt.Errorf("health check: %w", err)
	}

	// 9. Update Caddy manager FIRST (new port before old container stops)
	if d.caddyManager != nil && d.caddyManager.IsRunning() {
		if err := d.caddyManager.UpdatePortSnippets(app.ID, oldPort, hostPort); err != nil {
			return nil, fmt.Errorf("caddy port update: %w", err)
		}
	}

	// 10. Stop old container and remove it (zero-downtime swap)
	if oldContainerID != "" {
		if err := d.runner.StopContainer(ctx, oldContainerID); err != nil {
			log.Printf("warning: stop old container %s: %v", oldContainerID[:12], err)
		}
		if err := d.runner.RemoveContainer(ctx, oldContainerID); err != nil {
			log.Printf("warning: remove old container %s: %v", oldContainerID[:12], err)
		}
	}

	// 11. Update app record with new container ID (atomic transaction)
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

	log.Printf("promoted %s -> %s (container=%s, port=%d)", appName, version, containerID[:12], hostPort)
	// Clean up old tarballs (non-fatal)
	if err := build.CleanOldTarballs(appName, 5); err != nil {
		log.Printf("warning: clean old tarballs: %v", err)
	}
	audit.Log(audit.Entry{
		Time:       time.Now().UTC(),
		Action:     "promote",
		App:        appName,
		Version:    version,
		DurationMs: time.Since(startTime).Milliseconds(),
		Result:     "ok",
	})
	return &types.PromoteResponse{
		Message:       fmt.Sprintf("promoted %s to %s in %.0fs", appName, version, time.Since(dep.CreatedAt).Seconds()),
		Version:       version,
		NewContainerID: containerID,
		Port:          hostPort,
		OldContainerID: oldContainerID,
	}, nil
}

func (d *Deployer) buildImage(ctx context.Context, contextDir, dockerfilePath, imageRef string) (*buildResult, error) {
	// Create a tar of the build context with the Dockerfile
	buildContext, err := build.CreateBuildContext(contextDir, dockerfilePath)
	if err != nil {
		return nil, fmt.Errorf("create build context: %w", err)
	}
	defer buildContext.Close()

	buildResp, err := d.client.ImageBuild(ctx, buildContext, moby.ImageBuildOptions{
		Tags:           []string{imageRef},
		Dockerfile:     filepath.Base(dockerfilePath),
		SuppressOutput: true,
		Remove:         true,
		ForceRemove:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("docker build: %w", err)
	}
	defer buildResp.Body.Close()

	// Drain the build output
	if _, err := io.Copy(io.Discard, buildResp.Body); err != nil {
		return nil, fmt.Errorf("read build output: %w", err)
	}

	return &buildResult{
		Version:     strings.Split(imageRef, ":")[1],
		ImageRef:    imageRef,
		ImageDigest: "",
	}, nil
}
// buildResult holds the result of a successful image build.
type buildResult struct {
	Version     string
	ImageRef    string
	ImageDigest string
}

// createPromoteContainer sets up the container config for a new deployment.
func (d *Deployer) createPromoteContainer(ctx context.Context, app *types.App, imageRef string, env []string, hostPort, svcPort int, version string) (string, error) {
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
		Port:  hostPort,
		Image: imageRef,
		Env:   envMap,
	}
	return d.runner.CreateContainer(ctx, promoteApp, version)
}
// mergeEnv combines app env vars with decrypted secrets. Secrets override app env on conflict.
func mergeEnv(appEnv map[string]string, secrets map[string]string) []string {
	merged := make(map[string]string)

	// Start with app env
	for k, v := range appEnv {
		merged[k] = v
	}

	// Secrets override app env
	for k, v := range secrets {
		merged[k] = v
	}

	// Convert to KEY=VALUE format
	env := make([]string, 0, len(merged))
	for k, v := range merged {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
