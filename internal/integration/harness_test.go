//go:build integration

// Package integration contains Docker-backed integration tests for the deploy
// core loop (promote, rollback, start/stop, caddy conf, prune, dev containers).
//
// These tests are hermetic: each test sets DEPLOY_DATA_DIR to a fresh temp dir,
// runs a real in-process API server against the real Docker daemon, and never
// touches the production data dir (/mnt/bigvolume/.deploy) or socket
// (/var/run/deploy/deploy.sock). Run with:
//
//	make test-integration
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deploy/internal/api"
	"deploy/internal/caddyfile"
	deployclient "deploy/internal/client"
	"deploy/internal/config"
	"deploy/internal/deploy"
	"deploy/internal/runner"
	"deploy/internal/scheduler"
	"deploy/internal/state"
	"deploy/internal/types"

	"github.com/google/uuid"
	mobyclient "github.com/moby/moby/client"
)

const testMasterKey = "0123456789abcdef0123456789abcdef"

// harness holds the full in-process deploy stack: DB, Docker runner, deployer,
// caddy manager, scheduler, API server, and client.
type harness struct {
	t            *testing.T
	dataDir      string
	db           *sql.DB
	dockerRunner *runner.DockerRunner
	dockerClient *mobyclient.Client
	sched        *scheduler.Scheduler
	deployer     *deploy.Deployer
	cm           *caddyfile.CaddyManager
	srv          *api.Server
	deployClient *deployclient.Client
	socketPath   string
	appNames     []string
}

// newHarness boots a hermetic deploy stack. It skips the test if the Docker
// daemon is unreachable so the suite degrades gracefully on non-Docker hosts.
func newHarness(t *testing.T) *harness {
	t.Helper()
	skipIfDockerUnavailable(t)

	dataDir := t.TempDir()
	t.Setenv("DEPLOY_DATA_DIR", dataDir)

	if err := config.InitDir(); err != nil {
		t.Fatalf("init deploy dir: %v", err)
	}

	masterKey, err := state.EnsureMasterKey(config.DeployDirPath())
	if err != nil {
		t.Fatalf("ensure master key: %v", err)
	}

	db, err := state.OpenDB(config.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := state.Migrate(db); err != nil {
		db.Close()
		t.Fatalf("migrate: %v", err)
	}

	dockerRunner, err := runner.NewDockerRunner()
	if err != nil {
		db.Close()
		t.Fatalf("create docker runner: %v", err)
	}
	dockerClient, err := mobyclient.NewClientWithOpts(mobyclient.FromEnv, mobyclient.WithAPIVersionNegotiation())
	if err != nil {
		db.Close()
		t.Fatalf("create docker client: %v", err)
	}

	sched := scheduler.New(db)
	lockManager := deploy.NewLockManager()
	deployer := deploy.NewDeployer(dockerRunner, dockerClient, db, lockManager, masterKey)

	// Fake caddy: a sleep-loop script so the manager reports "running" and
	// reloads regenerate snippet files without binding any real socket.
	fakeCaddy := filepath.Join(dataDir, "fake-caddy")
	writeFakeCaddy(t, fakeCaddy)
	cm := caddyfile.NewCaddyManager(db, config.CaddyDir())
	cm.SetCaddyBin(fakeCaddy)
	if err := cm.Start(); err != nil {
		t.Fatalf("start caddy manager: %v", err)
	}
	deployer.SetCaddyManager(cm)

	socketPath := filepath.Join(dataDir, "deploy.sock")
	srv := api.NewServer(db, dockerRunner, sched, deployer, cm, socketPath, masterKey)
	go func() {
		_ = srv.ListenAndServe()
	}()
	waitForSocket(t, socketPath)

	h := &harness{
		t:            t,
		dataDir:      dataDir,
		db:           db,
		dockerRunner: dockerRunner,
		dockerClient: dockerClient,
		sched:        sched,
		deployer:     deployer,
		cm:           cm,
		srv:          srv,
		deployClient: deployclient.New(socketPath),
		socketPath:   socketPath,
	}
	t.Cleanup(h.cleanup)
	return h
}

// skipIfDockerUnavailable probes the Docker daemon and skips the test when it
// cannot be reached.
func skipIfDockerUnavailable(t *testing.T) {
	t.Helper()
	dr, err := runner.NewDockerRunner()
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := dr.ListContainers(ctx); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server socket %s not ready after 1s", socketPath)
}

func writeFakeCaddy(t *testing.T, path string) {
	t.Helper()
	// exec tail -f /dev/null: a single long-lived process with no children.
	// The CaddyManager restarts its caddy subprocess on every reload and kills
	// only the direct child; a shell with a sleep child would leave the sleep
	// orphaned holding the test stdout pipe, which makes `go test` report
	// "Test I/O incomplete" on exit.
	script := "#!/bin/sh\nexec tail -f /dev/null\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake caddy: %v", err)
	}
}

// cleanup tears down the stack and removes only the containers this harness
// created (identified by the unique deploy.app.name label each test uses).
func (h *harness) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, name := range h.appNames {
		h.removeAppContainers(ctx, name)
	}

	if h.cm != nil && h.cm.IsRunning() {
		_ = h.cm.Stop()
	}
	if h.srv != nil {
		_ = h.srv.Shutdown(ctx)
	}
	if h.sched != nil {
		h.sched.Stop()
	}
	if h.db != nil {
		_ = h.db.Close()
	}
}

func (h *harness) removeAppContainers(ctx context.Context, appName string) {
	f := make(mobyclient.Filters)
	f.Add("label", "deploy.app.name="+appName)
	list, err := h.dockerClient.ContainerList(ctx, mobyclient.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		h.t.Logf("cleanup: list containers for %s: %v", appName, err)
		return
	}
	for _, c := range list.Items {
		timeout := 5
		_, _ = h.dockerClient.ContainerStop(ctx, c.ID, mobyclient.ContainerStopOptions{Timeout: &timeout})
		_, _ = h.dockerClient.ContainerRemove(ctx, c.ID, mobyclient.ContainerRemoveOptions{Force: true})
	}
}

// newAppName returns a unique, valid app name and tracks it for cleanup.
func (h *harness) newAppName() string {
	name := "it" + strings.ReplaceAll(uuid.New().String(), "-", "")[:12]
	h.appNames = append(h.appNames, name)
	return name
}

// freePort reserves a host port by binding 127.0.0.1:0 and releasing it, so
// tests can pass an explicit host port into deploy.yml and stay deterministic
// regardless of what the real daemon or other host services have bound.
func (h *harness) freePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 25; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		if port >= 20000 && port <= 60000 {
			return port
		}
	}
	t.Fatal("could not allocate a free port in range")
	return 0
}

const nginxHealthyDockerfile = `FROM nginx:alpine
COPY health /usr/share/nginx/html/health
`

const nginxBrokenDockerfile = `FROM nginx:alpine
`

// writeFixture writes a deploy.yml + Dockerfile app project into a temp dir and
// returns the dir. The health marker is served at /health by the built image
// (the broken Dockerfile variant serves nothing, so the health check fails).
func (h *harness) writeFixture(t *testing.T, appName, marker string) string {
	t.Helper()
	return h.writeFixtureWithDockerfile(t, appName, marker, nginxHealthyDockerfile)
}

func (h *harness) writeFixtureWithDockerfile(t *testing.T, appName, marker, dockerfile string) string {
	t.Helper()
	dir := t.TempDir()
	port := h.freePort(t)
	yml := fmt.Sprintf(`app: %s
build:
  context: .
  dockerfile: Dockerfile
ports:
  - container: 80
    host: %d
health:
  path: /health
  initial_delay: 1s
  interval: 1s
  timeout: 2s
  retries: 3
`, appName, port)
	if err := os.WriteFile(filepath.Join(dir, "deploy.yml"), []byte(yml), 0o644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "health"), []byte(marker), 0o644); err != nil {
		t.Fatalf("write health: %v", err)
	}
	return dir
}

// promote runs a synchronous promote via the streaming endpoint (the
// wait=true path is broken/SSE-only and not suitable for tests).
func (h *harness) promote(t *testing.T, appName, dir string) *types.PromoteResponse {
	t.Helper()
	resp, err := h.deployClient.PromoteStream(appName, dir, nil)
	if err != nil {
		t.Fatalf("promote %s: %v", appName, err)
	}
	return resp
}

func (h *harness) promoteErr(t *testing.T, appName, dir string) error {
	t.Helper()
	_, err := h.deployClient.PromoteStream(appName, dir, nil)
	return err
}

func (h *harness) mustGetApp(t *testing.T, name string) *types.App {
	t.Helper()
	app, err := h.deployClient.GetApp(name)
	if err != nil {
		t.Fatalf("get app %s: %v", name, err)
	}
	if app == nil {
		t.Fatalf("app %s not found", name)
	}
	return app
}

// assertHealth polls the app's /health endpoint until it serves a body
// containing want (the marker embedded in the image) or the deadline passes.
func (h *harness) assertHealth(t *testing.T, port int, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		body, err := h.httpGetBody(port, "/health")
		if err == nil && strings.Contains(string(body), want) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("health endpoint on port %d did not serve marker %q", port, want)
}

func (h *harness) httpGetBody(port int, path string) ([]byte, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return body, fmt.Errorf("status %d", resp.StatusCode)
	}
	return body, nil
}

// readSnippet returns the Caddy site snippet for a domain.
func (h *harness) readSnippet(t *testing.T, appName, domain string) string {
	t.Helper()
	path := filepath.Join(config.CaddyDir(), "sites", caddyfile.SiteFilename(appName, domain))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snippet %s: %v", path, err)
	}
	return string(data)
}

// containerBindsPort reports whether a container exposes the given host port.
func (h *harness) containerBindsPort(t *testing.T, id string, port int) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	insp, err := h.dockerClient.ContainerInspect(ctx, id, mobyclient.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("inspect container %s: %v", id, err)
	}
	want := fmt.Sprintf("%d", port)
	for _, bindings := range insp.Container.NetworkSettings.Ports {
		for _, b := range bindings {
			if b.HostPort == want {
				return true
			}
		}
	}
	return false
}

// containerWithLabels returns the first container (any state) matching all
// given label key=value pairs, or "" when none exists.
func (h *harness) containerWithLabels(t *testing.T, labels ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f := make(mobyclient.Filters)
	for _, kv := range labels {
		f.Add("label", kv)
	}
	list, err := h.dockerClient.ContainerList(ctx, mobyclient.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		t.Fatalf("list containers by labels %v: %v", labels, err)
	}
	if len(list.Items) == 0 {
		return ""
	}
	return list.Items[0].ID
}
