package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deploy/internal/runner"
	"deploy/internal/state"
	"deploy/internal/types"

	moby "github.com/moby/moby/client"
	"github.com/google/uuid"
)

// testDockerClient implements the dockerClient interface for testing.
type testDockerClient struct {
	mu              sync.Mutex
	containers      map[string]*testContainer
	imageBuildCalls int
	savedImages     map[string][]byte
	shouldFail      map[string]error
}

type testContainer struct {
	ID      string
	Image   string
	Env     []string
	Ports   map[int]int
	Labels  map[string]string
	Running bool
	Status  string
}

func newTestDockerClient() *testDockerClient {
	return &testDockerClient{
		containers:  make(map[string]*testContainer),
		savedImages: make(map[string][]byte),
		shouldFail:  make(map[string]error),
	}
}

// testReadCloser wraps a reader for use as io.ReadCloser.
type testReadCloser struct {
	reader io.Reader
}

func (t *testReadCloser) Read(p []byte) (n int, err error) {
	return t.reader.Read(p)
}
func (t *testReadCloser) Close() error { return nil }

func (c *testDockerClient) ImageBuild(ctx context.Context, buildContext io.Reader, opts moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.shouldFail["build"]; err != nil {
		return moby.ImageBuildResult{}, err
	}

	c.imageBuildCalls++

	// Drain the build context
	io.Copy(io.Discard, buildContext)

	// Return a result with a discard reader
	return moby.ImageBuildResult{
		Body: io.NopCloser(strings.NewReader("")),
	}, nil
}

func (c *testDockerClient) ImageSave(ctx context.Context, images []string, opts ...moby.ImageSaveOption) (moby.ImageSaveResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.shouldFail["save"]; err != nil {
		return nil, err
	}

	imageRef := images[0]
	data := []byte("tarball:" + imageRef)
	c.savedImages[imageRef] = data

	reader, writer := io.Pipe()
	go func() {
		writer.Write(data)
		writer.Close()
	}()

	return &testReadCloser{reader: reader}, nil
}

func (c *testDockerClient) ImageLoad(ctx context.Context, input io.Reader, opts ...moby.ImageLoadOption) (moby.ImageLoadResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.shouldFail["load"]; err != nil {
		return nil, err
	}

	data, _ := io.ReadAll(input)
	ref := strings.TrimPrefix(string(data), "tarball:")
	c.savedImages[ref] = data

	return &testReadCloser{reader: strings.NewReader("loaded " + ref)}, nil
}

func (c *testDockerClient) ImageInspect(ctx context.Context, imageID string, opts ...moby.ImageInspectOption) (moby.ImageInspectResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.shouldFail["inspect"]; err != nil {
		return moby.ImageInspectResult{}, err
	}

	// Set ID via promoted field on the embedded InspectResponse
	var result moby.ImageInspectResult
	result.ID = imageID + "-sha256-digest"
	return result, nil
}

func setupDeployTest(t *testing.T) (*testMocks, string) {
	t.Helper()

	db, err := state.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := state.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	mocks := &testMocks{
		runner:      runner.NewMockDocker(),
		docker:      newTestDockerClient(),
		db:          db,
		lockManager: NewLockManager(),
	}

	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "test-app",
		Port:  8080,
		Image: "nginx:latest",
		Env:   map[string]string{"NODE_ENV": "production"},
	}
	if _, err := state.CreateApp(db, app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	appDir := t.TempDir()
	deployYML := fmt.Sprintf(`app: %s
stack: dockerfile
build:
  context: .
  dockerfile: Dockerfile
health:
  path: /health
ports:
  - container: 80
    host: 8081
`, app.Name)

	if err := os.WriteFile(filepath.Join(appDir, "deploy.yml"), []byte(deployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}

	dockerfile := `FROM alpine:latest
CMD ["echo", "hello"]
`
	if err := os.WriteFile(filepath.Join(appDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	return mocks, appDir
}

type testMocks struct {
	runner      *runner.MockDocker
	docker      *testDockerClient
	db          *sql.DB
	lockManager *LockManager
}

// testMasterKey is a deterministic key for deploy tests.
var testMasterKey = []byte{
	0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04,
	0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c,
	0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14,
	0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c,
}

func newTestDeployer(m *testMocks) *Deployer {
	dep := NewDeployer(m.runner, m.docker, m.db, m.lockManager, testMasterKey)
	// Use a mock health check that always passes
	dep.SetHealthCheckFunc(func(ctx context.Context, port int, path string, initialDelay, interval, timeout time.Duration, retries int) error {
		return nil
	})
	return dep
}

func TestPromoteSuccess(t *testing.T) {
	mocks, appDir := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	// Create a running old container using the runner mock
	app, _ := state.GetAppByName(mocks.db, "test-app")
	oldApp := &types.App{
		ID:    app.ID,
		Name:  app.Name,
		Port:  app.Port,
		Image: "nginx:latest",
	}
	oldCID, err := mocks.runner.CreateContainer(context.Background(), oldApp, "v0.0.0")
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if err := mocks.runner.StartContainer(context.Background(), oldCID); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	// Update the app record to point to the old container
	if err := state.UpdateAppContainer(mocks.db, "test-app", oldCID); err != nil {
		t.Fatalf("UpdateAppContainer: %v", err)
	}

	// Promote should succeed, replacing old container
	resp, err := dep.Promote(context.Background(), &types.PromoteRequest{}, "test-app", appDir, nil)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Version == "" {
		t.Error("expected non-empty version")
	}
	if resp.NewContainerID == "" {
		t.Error("expected non-empty container ID")
	}
	if resp.Port == 0 {
		t.Error("expected non-zero port")
	}

	// Verify old container was removed
	_, err = mocks.runner.InspectContainer(context.Background(), oldCID)
	if err == nil {
		t.Error("expected old container to be removed")
	}
}

func TestPromoteWithSecrets(t *testing.T) {
	mocks, appDir := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	app, _ := state.GetAppByName(mocks.db, "test-app")

	// Set a secret via the state package
	secret := &types.Secret{
		AppID: app.ID,
		Key:   "DB_PASSWORD",
		Value: "supersecret",
	}
	if _, err := state.SetSecret(mocks.db, secret, testMasterKey); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	// Promote
	resp, err := dep.Promote(context.Background(), &types.PromoteRequest{}, "test-app", appDir, nil)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Check that the container got the merged env (app env + secret)
	mocks.runner.Mu.Lock()
	defer mocks.runner.Mu.Unlock()

	var found bool
	for _, c := range mocks.runner.Containers {
		if c.ID == resp.NewContainerID {
			if c.App.Env["DB_PASSWORD"] == "supersecret" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected DB_PASSWORD=supersecret in container env")
	}
}

func TestPromoteBuildFailure(t *testing.T) {
	mocks, appDir := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	mocks.docker.shouldFail["build"] = fmt.Errorf("build failed")

	_, err := dep.Promote(context.Background(), &types.PromoteRequest{}, "test-app", appDir, nil)
	if err == nil {
		t.Fatal("expected error for build failure")
	}
	if !strings.Contains(err.Error(), "build") {
		t.Errorf("expected build-related error, got: %v", err)
	}
}

func TestPromoteHealthCheckFailure(t *testing.T) {
	mocks, appDir := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	// Make the runner's HealthCheck fail for any container
	mocks.runner.ShouldFail["HealthCheck"] = fmt.Errorf("health check failed")

	_, err := dep.Promote(context.Background(), &types.PromoteRequest{}, "test-app", appDir, nil)
	if err == nil {
		t.Fatal("expected error for health check failure")
	}
	if !strings.Contains(err.Error(), "health") {
		t.Errorf("expected health-check-related error, got: %v", err)
	}
}

func TestMergeEnv(t *testing.T) {
	appEnv := map[string]string{
		"NODE_ENV": "production",
		"PORT":     "8080",
	}
	secrets := map[string]string{
		"DB_PASSWORD": "secret123",
		"NODE_ENV":    "should-not-appear", // secrets override app env
	}

	env := mergeEnv(appEnv, secrets)

	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if envMap["NODE_ENV"] != "should-not-appear" {
		t.Errorf("expected secrets to override NODE_ENV, got %q", envMap["NODE_ENV"])
	}
	if envMap["PORT"] != "8080" {
		t.Errorf("expected PORT=8080, got %q", envMap["PORT"])
	}
	if envMap["DB_PASSWORD"] != "secret123" {
		t.Errorf("expected DB_PASSWORD=secret123, got %q", envMap["DB_PASSWORD"])
	}
}

func TestPromoteAppNotFound(t *testing.T) {
	mocks, _ := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	_, err := dep.Promote(context.Background(), &types.PromoteRequest{}, "nonexistent", ".", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent app")
	}
}
