package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deploy/internal/runner"
	"deploy/internal/caddyfile"
	"deploy/internal/deploy"
	"deploy/internal/scheduler"
	"deploy/internal/state"
	"deploy/internal/types"

	"github.com/google/uuid"
)

func setupTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	db, err := state.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := state.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	mockRunner := runner.NewMockDocker()
	sched := scheduler.New(db)

	socketPath := filepath.Join(os.TempDir(), "deploy-test-"+uuid.New().String()+".sock")
	testMasterKey := []byte("0123456789abcdef0123456789abcdef")
	server := NewServer(db, mockRunner, sched, (*deploy.Deployer)(nil), (*caddyfile.CaddyManager)(nil), socketPath, testMasterKey)
	return server, socketPath
}

func startTestServer(t *testing.T) (*Server, string) {
	server, socketPath := setupTestServer(t)
	go func() {
		if err := server.ListenAndServe(); err != nil {
			t.Logf("server stopped: %v", err)
		}
	}()

	// Wait for socket to be ready
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			return server, socketPath
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("server socket %s not ready after 250ms", socketPath)
	return server, socketPath
}

func httpDo(t *testing.T, socketPath, method, path string, body io.Reader) *http.Response {
	t.Helper()
	client := http.Client{
		Transport: &http.Transport{
			Dial: func(network, addr string) (net.Conn, error) {
				return net.DialTimeout("unix", socketPath, 2*time.Second)
			},
		},
	}

	req, err := http.NewRequest(method, "http://unix"+path, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(data)
}

func TestHealthEndpoint(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	resp := httpDo(t, socketPath, "GET", "/api/v1/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var health types.HealthResponse
	if err := json.Unmarshal([]byte(readBody(t, resp)), &health); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if health.Status != "ok" {
		t.Errorf("expected ok, got %s", health.Status)
	}
}

func TestCreateApp(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	body := types.CreateAppRequest{
		Name:  "test-app",
		Port:  8080,
		Image: "nginx:latest",
		Env:   map[string]string{"KEY": "val"},
	}

	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)

	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var app types.App
	if err := json.Unmarshal([]byte(readBody(t, resp)), &app); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if app.Name != "test-app" {
		t.Errorf("expected test-app, got %s", app.Name)
	}
	if app.Port != 8080 {
		t.Errorf("expected 8080, got %d", app.Port)
	}
	if app.Env["KEY"] != "val" {
		t.Errorf("expected val, got %s", app.Env["KEY"])
	}
}

func TestDuplicateAppReturns409(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	body := types.CreateAppRequest{Name: "dup-app", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)

	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	buf.Reset()
	json.NewEncoder(&buf).Encode(body)
	resp = httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestGetAppReturns404(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	resp := httpDo(t, socketPath, "GET", "/api/v1/apps/nonexistent", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestCreateGetListDeleteCycle(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	body := types.CreateAppRequest{Name: "cycle-test", Port: 3000, Image: "node:20"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/cycle-test", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	resp = httpDo(t, socketPath, "GET", "/api/v1/apps", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	var listResp types.ListAppsResponse
	json.Unmarshal([]byte(readBody(t, resp)), &listResp)
	if len(listResp.Apps) != 1 {
		t.Errorf("expected 1 app, got %d", len(listResp.Apps))
	}

	resp = httpDo(t, socketPath, "DELETE", "/api/v1/apps/cycle-test", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("delete: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/cycle-test", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestErrorResponseFormat(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	resp := httpDo(t, socketPath, "GET", "/api/v1/apps/missing", nil)
	var errResp types.ErrorResponse
	json.Unmarshal([]byte(readBody(t, resp)), &errResp)

	if errResp.Code != types.ErrNotFound {
		t.Errorf("expected %s, got %s", types.ErrNotFound, errResp.Code)
	}
	if errResp.Error == "" {
		t.Errorf("expected error message")
	}
}

func TestStartStopWithMockRunner(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	body := types.CreateAppRequest{Name: "run-test", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/run-test/start?wait=true", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("start: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var startResp types.StartStopResponse
	json.Unmarshal([]byte(readBody(t, resp)), &startResp)
	if startResp.Container == "" {
		t.Errorf("expected container ID, got empty")
	}

	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/run-test", nil)
	var app types.App
	json.Unmarshal([]byte(readBody(t, resp)), &app)
	if app.Status != types.StatusRunning {
		t.Errorf("expected running, got %s", app.Status)
	}

	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/run-test/stop", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("stop: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/run-test", nil)
	json.Unmarshal([]byte(readBody(t, resp)), &app)
	if app.Status != types.StatusStopped {
		t.Errorf("expected stopped, got %s", app.Status)
	}
}

// --- Phase 2 Integration Tests ---

func TestPromoteReturnsErrorWhenDeployerNil(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create an app first
	body := types.CreateAppRequest{Name: "promote-test", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Promote with no deployer should fail
	buf.Reset()
	json.NewEncoder(&buf).Encode(map[string]string{"dir": "."})
	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/promote-test/promote", &buf)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected error when deployer is nil, got 200")
	}
}

func TestRollbackReturnsErrorWhenDeployerNil(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create an app first
	body := types.CreateAppRequest{Name: "rollback-test", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Rollback with no deployer should fail
	buf.Reset()
	json.NewEncoder(&buf).Encode(types.RollbackRequest{Version: "v1.0.0"})
	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/rollback-test/rollback", &buf)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected error when deployer is nil, got 200")
	}
}

func TestStatusReturnsErrorWhenDeployerNil(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create an app first
	body := types.CreateAppRequest{Name: "status-test", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Status with no deployer should fail
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/status-test/status", nil)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected error when deployer is nil, got 200")
	}
}

func TestGlobalStatusEndpoint(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Should return empty list when no apps
	resp := httpDo(t, socketPath, "GET", "/api/v1/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var gs types.GlobalStatusResponse
	if err := json.Unmarshal([]byte(readBody(t, resp)), &gs); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if gs.Apps == nil {
		t.Error("expected non-nil apps list")
	}
}

func TestGlobalStatusWithApps(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create an app
	body := types.CreateAppRequest{Name: "glob-status-app", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// Check global status
	resp = httpDo(t, socketPath, "GET", "/api/v1/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var gs types.GlobalStatusResponse
	json.Unmarshal([]byte(readBody(t, resp)), &gs)
	if len(gs.Apps) != 1 {
		t.Errorf("expected 1 app, got %d", len(gs.Apps))
	}
	if gs.Apps[0].App.Name != "glob-status-app" {
		t.Errorf("expected 'glob-status-app', got %s", gs.Apps[0].App.Name)
	}
}

func TestSecretsCRUD(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create app
	body := types.CreateAppRequest{Name: "secret-app", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// Set a secret
	buf.Reset()
	json.NewEncoder(&buf).Encode(types.SetSecretRequest{Key: "MY_KEY", Value: "my-value"})
	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/secret-app/secrets", &buf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set secret: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// List secrets
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/secret-app/secrets", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list secrets: %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var lsResp types.ListSecretsResponse
	json.Unmarshal([]byte(readBody(t, resp)), &lsResp)
	if len(lsResp.Secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(lsResp.Secrets))
	}
	if lsResp.Secrets[0].Key != "MY_KEY" {
		t.Errorf("expected key MY_KEY, got %s", lsResp.Secrets[0].Key)
	}
	if lsResp.Secrets[0].Value != "<masked>" {
		t.Errorf("expected masked value, got %s", lsResp.Secrets[0].Value)
	}

	// Get secret (unmasked)
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/secret-app/secrets/MY_KEY", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get secret: %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var sec types.Secret
	json.Unmarshal([]byte(readBody(t, resp)), &sec)
	if sec.Value != "my-value" {
		t.Errorf("expected 'my-value', got %s", sec.Value)
	}

	// Remove secret
	resp = httpDo(t, socketPath, "DELETE", "/api/v1/apps/secret-app/secrets/MY_KEY", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove secret: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Verify removed
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/secret-app/secrets/MY_KEY", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestSecretsListEmpty(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create app
	body := types.CreateAppRequest{Name: "empty-secret-app", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// List secrets for app with none
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/empty-secret-app/secrets", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list secrets: %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var lsResp types.ListSecretsResponse
	json.Unmarshal([]byte(readBody(t, resp)), &lsResp)
	if len(lsResp.Secrets) != 0 {
		t.Errorf("expected 0 secrets, got %d", len(lsResp.Secrets))
	}
}

func TestSecretsAppNotFound(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Set secret for nonexistent app
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(types.SetSecretRequest{Key: "K", Value: "V"})
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps/nonexistent/secrets", &buf)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Get secret for nonexistent app
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/nonexistent/secrets/K", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}

	// Delete secret for nonexistent app
	resp = httpDo(t, socketPath, "DELETE", "/api/v1/apps/nonexistent/secrets/K", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestImagesListEndpoint(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// List images for an app that has none
	resp := httpDo(t, socketPath, "GET", "/api/v1/apps/test-app/images", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list images: %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var imgResp types.ListImagesResponse
	json.Unmarshal([]byte(readBody(t, resp)), &imgResp)
	if imgResp.Images == nil {
		t.Error("expected non-nil images list")
	}
}

func TestImagesRemoveEndpoint(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Remove a nonexistent tarball should still succeed (idempotent)
	resp := httpDo(t, socketPath, "DELETE", "/api/v1/apps/test-app/images/v1.0.0", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove image: %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestPromoteRequiresAppName(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	resp := httpDo(t, socketPath, "POST", "/api/v1/apps//promote", nil)
	if resp.StatusCode == http.StatusOK {
		t.Error("expected error for missing app name")
	}
}

func TestRollbackRequiresAppName(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	resp := httpDo(t, socketPath, "POST", "/api/v1/apps//rollback", nil)
	if resp.StatusCode == http.StatusOK {
		t.Error("expected error for missing app name")
	}
}

func TestGlobalStatusWithMultipleApps(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create two apps
	apps := []string{"multi-a", "multi-b"}
	for _, name := range apps {
		body := types.CreateAppRequest{Name: name, Port: 8080, Image: "nginx:latest"}
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: %d", name, resp.StatusCode)
		}
	}

	resp := httpDo(t, socketPath, "GET", "/api/v1/status", nil)
	var gs types.GlobalStatusResponse
	json.Unmarshal([]byte(readBody(t, resp)), &gs)
	if len(gs.Apps) != 2 {
		t.Errorf("expected 2 apps, got %d", len(gs.Apps))
	}
}

func TestSecretsSetEmptyKey(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	body := types.CreateAppRequest{Name: "sec-test", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// Set with empty key should fail
	buf.Reset()
	json.NewEncoder(&buf).Encode(types.SetSecretRequest{Key: "", Value: "val"})
	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/sec-test/secrets", &buf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty key, got %d", resp.StatusCode)
	}
}

// --- Domain Tests ---

func TestAddDomain(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create app first
	body := types.CreateAppRequest{Name: "domain-test", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// Add domain
	buf.Reset()
	json.NewEncoder(&buf).Encode(types.AddDomainRequest{Domain: "example.com"})
	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/domain-test/domains", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add domain: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var domain types.Domain
	if err := json.Unmarshal([]byte(readBody(t, resp)), &domain); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if domain.Domain != "example.com" {
		t.Errorf("expected example.com, got %s", domain.Domain)
	}
	if domain.ID == "" {
		t.Error("expected domain ID to be set")
	}
}

func TestAddDomainInvalidApp(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(types.AddDomainRequest{Domain: "example.com"})
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps/nonexistent/domains", &buf)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestListDomains(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create app
	body := types.CreateAppRequest{Name: "list-dom-test", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// Add two domains
	for _, d := range []string{"alpha.example.com", "beta.example.com"} {
		buf.Reset()
		json.NewEncoder(&buf).Encode(types.AddDomainRequest{Domain: d})
		resp = httpDo(t, socketPath, "POST", "/api/v1/apps/list-dom-test/domains", &buf)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("add domain %s: %d: %s", d, resp.StatusCode, readBody(t, resp))
		}
	}

	// List domains for app
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/list-dom-test/domains", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}

	var listResp types.ListDomainsResponse
	json.Unmarshal([]byte(readBody(t, resp)), &listResp)
	if len(listResp.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(listResp.Domains))
	}

	// Should be sorted by domain
	if listResp.Domains[0].Domain != "alpha.example.com" {
		t.Errorf("expected alpha first, got %s", listResp.Domains[0].Domain)
	}
	if listResp.Domains[1].Domain != "beta.example.com" {
		t.Errorf("expected beta second, got %s", listResp.Domains[1].Domain)
	}
}
func TestListDomainsGlobal(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create two apps with domains
	for _, appName := range []string{"global-dom-a", "global-dom-b"} {
		body := types.CreateAppRequest{Name: appName, Port: 8080, Image: "nginx:latest"}
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: %d", appName, resp.StatusCode)
		}

		// Add a domain to each app
		buf.Reset()
		domain := appName + ".example.com"
		json.NewEncoder(&buf).Encode(types.AddDomainRequest{Domain: domain})
		resp = httpDo(t, socketPath, "POST", "/api/v1/apps/"+appName+"/domains", &buf)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("add domain %s: %d: %s", domain, resp.StatusCode, readBody(t, resp))
		}
	}

	// List all domains globally — should return both
	resp := httpDo(t, socketPath, "GET", "/api/v1/domains", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list all domains: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var listResp types.ListDomainsResponse
	if err := json.Unmarshal([]byte(readBody(t, resp)), &listResp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(listResp.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(listResp.Domains))
	}

	// Verify app names are populated
	domains := map[string]string{}
	for _, d := range listResp.Domains {
		domains[d.Domain] = d.AppName
	}
	if domains["global-dom-a.example.com"] != "global-dom-a" {
		t.Errorf("expected app name global-dom-a, got %q", domains["global-dom-a.example.com"])
	}
	if domains["global-dom-b.example.com"] != "global-dom-b" {
		t.Errorf("expected app name global-dom-b, got %q", domains["global-dom-b.example.com"])
	}
}


func TestRemoveDomain(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create app
	body := types.CreateAppRequest{Name: "rm-dom-test", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// Add domain
	buf.Reset()
	json.NewEncoder(&buf).Encode(types.AddDomainRequest{Domain: "remove.example.com"})
	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/rm-dom-test/domains", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add: %d", resp.StatusCode)
	}

	// Remove domain
	resp = httpDo(t, socketPath, "DELETE", "/api/v1/apps/rm-dom-test/domains/remove.example.com", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Verify gone
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/rm-dom-test/domains", nil)
	var listResp types.ListDomainsResponse
	json.Unmarshal([]byte(readBody(t, resp)), &listResp)
	if len(listResp.Domains) != 0 {
		t.Errorf("expected 0 domains after remove, got %d", len(listResp.Domains))
	}
}

func TestRemoveDomainNotFound(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create app
	body := types.CreateAppRequest{Name: "rm-notfound", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// Remove nonexistent domain — should not error (idempotent)
	resp = httpDo(t, socketPath, "DELETE", "/api/v1/apps/rm-notfound/domains/nonexistent.example.com", nil)
	// The state.DeleteDomainByDomain is idempotent, so 204 is fine
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestAddDomainRequiresValidDomain(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	body := types.CreateAppRequest{Name: "valid-dom", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// Domain without dot should fail
	buf.Reset()
	json.NewEncoder(&buf).Encode(types.AddDomainRequest{Domain: "invalid"})
	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/valid-dom/domains", &buf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid domain, got %d", resp.StatusCode)
	}
}

func TestContainerLogsNonFollow(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create an app
	body := types.CreateAppRequest{Name: "log-test", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Start the app to get a container ID
	resp = httpDo(t, socketPath, "POST", "/api/v1/apps/log-test/start?wait=true", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Get logs (non-follow)
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/log-test/logs?tail=100&follow=false", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var entries []types.LogEntry
	if err := json.Unmarshal([]byte(readBody(t, resp)), &entries); err != nil {
		t.Fatalf("unmarshal log entries: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least one log entry")
	}
	if entries[0].Line == "" {
		t.Error("expected non-empty log line")
	}
	if entries[0].Stream == "" {
		t.Error("expected stream to be set")
	}
}

func TestContainerLogsReturns404ForMissingApp(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	resp := httpDo(t, socketPath, "GET", "/api/v1/apps/nonexistent/logs?tail=10&follow=false", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestContainerLogsReturnsEmptyForNoContainer(t *testing.T) {
	_, socketPath := startTestServer(t)
	defer os.Remove(socketPath)

	// Create an app but don't start it (no container)
	body := types.CreateAppRequest{Name: "no-container", Port: 8080, Image: "nginx:latest"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	resp := httpDo(t, socketPath, "POST", "/api/v1/apps", &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	// Get logs — should return empty array since no container
	resp = httpDo(t, socketPath, "GET", "/api/v1/apps/no-container/logs?tail=100&follow=false", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs: %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var entries []types.LogEntry
	if err := json.Unmarshal([]byte(readBody(t, resp)), &entries); err != nil {
		t.Fatalf("unmarshal log entries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for app with no container, got %d", len(entries))
	}
}
