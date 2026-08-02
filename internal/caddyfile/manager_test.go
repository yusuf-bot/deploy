package caddyfile

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deploy/internal/state"
	"deploy/internal/types"

	"github.com/google/uuid"
)

// setupTestDB creates an in-memory SQLite DB with migrations applied.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := state.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := state.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// createTestApp creates a test app with given name, port, and status.
func createTestApp(t *testing.T, db *sql.DB, name string, port int, status string) *types.App {
	t.Helper()
	app := &types.App{
		ID:     uuid.New().String(),
		Name:   name,
		Port:   port,
		Image:  "nginx:latest",
		Status: status,
	}
	created, err := state.CreateApp(db, app)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return created
}

// addTestDomain adds a domain for the given app.
func addTestDomain(t *testing.T, db *sql.DB, appID string, domain string) {
	t.Helper()
	d := &types.Domain{
		AppID:  appID,
		Domain: domain,
	}
	if err := state.CreateDomain(db, d); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
}

// TestMainCaddyfileContent tests the static main Caddyfile content.
func TestMainCaddyfileContent(t *testing.T) {
	content := MainCaddyfile()
	if !strings.Contains(content, "admin off") {
		t.Error("expected 'admin off' in main Caddyfile")
	}
	if !strings.Contains(content, "import sites/*.conf") {
		t.Error("expected 'import sites/*.conf' in main Caddyfile")
	}
	if !strings.Contains(content, "protocols h1 h2") {
		t.Error("expected 'protocols h1 h2' in main Caddyfile")
	}
}

// TestSiteBlockPublicDomain tests SiteBlock for a public domain.
func TestSiteBlockPublicDomain(t *testing.T) {
	block := SiteBlock("example.com", 8080)
	expected := "example.com {\n    reverse_proxy localhost:8080\n}\n"
	if block != expected {
		t.Errorf("unexpected site block:\ngot:\n%s\nwant:\n%s", block, expected)
	}
}

// TestSiteBlockLocalhostDomain tests SiteBlock for a *.localhost domain (should have tls internal).
func TestSiteBlockLocalhostDomain(t *testing.T) {
	block := SiteBlock("myapp.localhost", 8080)
	if !strings.Contains(block, "tls internal") {
		t.Error("expected 'tls internal' for localhost domain")
	}
	if !strings.Contains(block, "reverse_proxy localhost:8080") {
		t.Error("expected reverse_proxy directive")
	}
}

// TestSiteBlockInvalidPort tests SiteBlock with invalid port.
func TestSiteBlockInvalidPort(t *testing.T) {
	block := SiteBlock("example.com", 0)
	if !strings.Contains(block, "invalid port") {
		t.Error("expected error message for invalid port")
	}
}

// TestIsLocalDomain tests IsLocalDomain for various domains.
func TestIsLocalDomain(t *testing.T) {
	tests := []struct {
		domain string
		want   bool
	}{
		{"myapp.localhost", true},
		{"localhost", true},
		{"api.localhost", true},
		{"example.com", false},
		{"example.local", false},
		{"sub.example.com", false},
		{"", false},
	}
	for _, tt := range tests {
		got := IsLocalDomain(tt.domain)
		if got != tt.want {
			t.Errorf("IsLocalDomain(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

// TestSiteFilename tests that SiteFilename contains app name.
func TestSiteFilename(t *testing.T) {
	filename := SiteFilename("my-app", "example.com")
	if !strings.Contains(filename, "my-app") {
		t.Error("expected filename to contain app name")
	}
	if !strings.HasSuffix(filename, ".conf") {
		t.Error("expected filename to end with .conf")
	}
	if !strings.Contains(filename, "example-com") {
		t.Error("expected filename to contain sanitised domain")
	}
}

// TestSiteFilenameSanitization tests that SiteFilename handles special chars.
func TestSiteFilenameSanitization(t *testing.T) {
	filename := SiteFilename("My_App-1", "test.Example.COM:443")
	if !strings.Contains(filename, "My_App-1") {
		t.Error("expected filename to contain app name")
	}
	if strings.Contains(filename, ":") {
		t.Error("expected filename to not contain colon")
	}
}

// TestAddDomainSnippet tests that AddDomainSnippet writes correct file content.
func TestAddDomainSnippet(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "test-app", 8080, types.StatusRunning)
	addTestDomain(t, db, app.ID, "testapp.example.com")

	tmpDir := t.TempDir()
	mgr := NewCaddyManager(db, tmpDir)

	if err := mgr.AddDomainSnippet("test-app", "testapp.example.com", 8080, false); err != nil {
		t.Fatalf("AddDomainSnippet: %v", err)
	}

	// Check file was written
	sitesDir := filepath.Join(tmpDir, "sites")
	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one snippet file")
	}

	// Check file content
	data, err := os.ReadFile(filepath.Join(sitesDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "http://testapp.example.com {") {
		t.Error("expected http listener block for domain")
	}
	if !strings.Contains(content, "testapp.example.com {") {
		t.Error("expected https listener block for domain")
	}
	if !strings.Contains(content, "localhost:8080") {
		t.Error("expected port in snippet content")
	}
}

// TestRemoveDomainSnippet tests that RemoveDomainSnippet deletes the correct file.
func TestRemoveDomainSnippet(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "test-app", 8080, types.StatusRunning)
	addTestDomain(t, db, app.ID, "remove.example.com")

	tmpDir := t.TempDir()
	mgr := NewCaddyManager(db, tmpDir)

	// Add first
	if err := mgr.AddDomainSnippet("test-app", "remove.example.com", 8080, false); err != nil {
		t.Fatalf("AddDomainSnippet: %v", err)
	}

	// Remove
	if err := mgr.RemoveDomainSnippet("remove.example.com"); err != nil {
		t.Fatalf("RemoveDomainSnippet: %v", err)
	}

	// Verify removed
	sitesDir := filepath.Join(tmpDir, "sites")
	entries, _ := os.ReadDir(sitesDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "remove") {
			t.Error("expected snippet file to be removed")
		}
	}
}

// TestUpdatePortSnippets tests that UpdatePortSnippets updates port in files.
func TestUpdatePortSnippets(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "port-app", 8080, types.StatusRunning)
	addTestDomain(t, db, app.ID, "portapp.example.com")

	tmpDir := t.TempDir()
	mgr := NewCaddyManager(db, tmpDir)

	// Add snippet with port 8080
	if err := mgr.AddDomainSnippet("port-app", "portapp.example.com", 8080, false); err != nil {
		t.Fatalf("AddDomainSnippet: %v", err)
	}

	// Update port to 9090
	if err := mgr.UpdatePortSnippets(app.ID, 8080, 9090); err != nil {
		t.Fatalf("UpdatePortSnippets: %v", err)
	}

	// Verify content updated
	sitesDir := filepath.Join(tmpDir, "sites")
	entries, _ := os.ReadDir(sitesDir)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(sitesDir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		content := string(data)
		if strings.Contains(content, "localhost:8080") {
			t.Error("expected old port to be replaced")
		}
		if !strings.Contains(content, "localhost:9090") {
			t.Error("expected new port in content")
		}
	}
}

// TestStartFailsOnMissingBinary tests that Start fails when caddy binary is missing.
func TestStartFailsOnMissingBinary(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()
	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin("/nonexistent/caddy-binary-xxxx")

	err := mgr.Start()
	if err == nil {
		t.Fatal("expected error when caddy binary missing, got nil")
	}
	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestStartStopLifecycle tests Start/Stop lifecycle with a fake caddy script.
func TestStartStopLifecycle(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	// Create a fake caddy script that sleeps until signalled
	fakeCaddy := filepath.Join(tmpDir, "fake-caddy.sh")
	script := `#!/bin/sh
# Fake caddy: sleep until killed, print a startup message
echo "Caddy starting"
while [ 1 ]; do sleep 1; done
`
	if err := os.WriteFile(fakeCaddy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile fake caddy: %v", err)
	}

	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin(fakeCaddy)

	// Start
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !mgr.IsRunning() {
		t.Fatal("expected IsRunning to be true after Start")
	}

	// Stop
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if mgr.IsRunning() {
		t.Fatal("expected IsRunning to be false after Stop")
	}
}

// TestReloadRestartsProcess verifies that Reload restarts the Caddy process
// with a fresh PID so it re-reads the freshly generated config. Caddy ignores
// SIGHUP, so a restart (via the explicit --config flag in startProcess) is
// how config changes are actually applied.
func TestReloadRestartsProcess(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	// Fake caddy that records each instance's PID.
	pidFile := filepath.Join(tmpDir, "caddy-pids")
	fakeCaddy := filepath.Join(tmpDir, "fake-caddy-restart.sh")
	script := "#!/bin/sh\n# Fake caddy that records each instance's PID\necho \"Caddy starting\"\necho $$ >> " + pidFile + "\nwhile [ 1 ]; do sleep 1; done\n"
	if err := os.WriteFile(fakeCaddy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile fake caddy: %v", err)
	}

	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin(fakeCaddy)

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.Stop()

	// Wait for the first instance to record its PID.
	waitForPIDCount(t, pidFile, 1)

	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Reload must start a brand-new process (new PID) with the fresh config.
	waitForPIDCount(t, pidFile, 2)
	pids := readPIDFile(t, pidFile)
	if pids[0] == pids[1] {
		t.Fatalf("expected Reload to restart caddy with a new PID, got %s both times", pids[0])
	}
}

// waitForPIDCount polls the pidfile until it contains at least n entries.
func waitForPIDCount(t *testing.T, path string, n int) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if len(readPIDFile(t, path)) >= n {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected pidfile %s to contain %d entries within timeout", path, n)
}

// readPIDFile reads the pidfile into a slice of PID strings.
// Returns nil while the file does not exist yet.
func readPIDFile(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("ReadFile pidfile: %v", err)
	}
	return strings.Fields(string(data))
}

// TestGenerateSnippetsFromDB tests that generateSnippets creates files for running apps with domains.
func TestGenerateSnippetsFromDB(t *testing.T) {
	db := setupTestDB(t)
	app1 := createTestApp(t, db, "alpha", 8080, types.StatusRunning)
	app2 := createTestApp(t, db, "beta", 9090, types.StatusRunning)
	_ = createTestApp(t, db, "gamma", 7070, types.StatusCreated) // not running — should be skipped

	addTestDomain(t, db, app1.ID, "alpha.example.com")
	addTestDomain(t, db, app2.ID, "beta.localhost")

	tmpDir := t.TempDir()
	mgr := NewCaddyManager(db, tmpDir)

	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	sitesDir := filepath.Join(tmpDir, "sites")
	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 snippet files, got %d", len(entries))
	}

	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(sitesDir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		content := string(data)

		if strings.Contains(e.Name(), "alpha") {
			if !strings.Contains(content, "http://alpha.example.com {") {
				t.Error("expected http listener block for alpha")
			}
			if !strings.Contains(content, "alpha.example.com {") {
				t.Error("expected https listener block for alpha")
			}
			if !strings.Contains(content, "localhost:8080") {
				t.Error("expected alpha port 8080")
			}
		}
		if strings.Contains(e.Name(), "beta") {
			if !strings.Contains(content, "beta.localhost") {
				t.Error("expected beta domain")
			}
			if !strings.Contains(content, "tls internal") {
				t.Error("expected tls internal for localhost")
			}
			if !strings.Contains(content, "localhost:9090") {
				t.Error("expected beta port 9090")
			}
		}
	}
}

// TestGenerateSnippetsCleansUp tests that removed domains have their snippet files cleaned up.
func TestGenerateSnippetsCleansUp(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "cleanup", 8080, types.StatusRunning)
	addTestDomain(t, db, app.ID, "cleanup.example.com")

	tmpDir := t.TempDir()
	mgr := NewCaddyManager(db, tmpDir)

	// Generate initial snippets
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Delete the domain from DB and regenerate
	if err := state.DeleteDomainByDomain(db, "cleanup.example.com"); err != nil {
		t.Fatalf("DeleteDomainByDomain: %v", err)
	}

	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	sitesDir := filepath.Join(tmpDir, "sites")
	entries, _ := os.ReadDir(sitesDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "cleanup") {
			t.Error("expected snippet file to be cleaned up")
		}
	}
}

// TestDoubleStartFails tests that starting twice returns an error.
func TestDoubleStartFails(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	fakeCaddy := filepath.Join(tmpDir, "fake-caddy-double.sh")
	script := `#!/bin/sh
echo "Caddy starting"
while [ 1 ]; do sleep 1; done
`
	if err := os.WriteFile(fakeCaddy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin(fakeCaddy)

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.Stop()

	err := mgr.Start()
	if err == nil {
		t.Fatal("expected error on double start")
	}
}

// TestStopWithoutStart tests that Stop doesn't error when not running.
func TestStopWithoutStart(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()
	mgr := NewCaddyManager(db, tmpDir)

	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop without start: %v", err)
	}
}

// TestSignal0Check tests isProcessAlive signal 0 check.
func TestSignal0Check(t *testing.T) {
	// Our own PID should always be alive
	if !isProcessAlive(os.Getpid()) {
		t.Error("expected current process to be alive")
	}

	// PID 0 should fail
	if isProcessAlive(0) {
		// On some platforms PID 0 might exist, but typically it won't
		// Just don't fail here
	}
}

// TestRemoveOrphanedSnippet tests removing a snippet for a domain not in DB.
func TestRemoveOrphanedSnippet(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	mgr := NewCaddyManager(db, tmpDir)

	// Write an orphaned snippet file manually
	sitesDir := filepath.Join(tmpDir, "sites")
	if err := os.MkdirAll(sitesDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	orphanPath := filepath.Join(sitesDir, "orphan-test.conf")
	orphanContent := "orphan.example.com {\n    reverse_proxy localhost:9999\n}\n"
	if err := os.WriteFile(orphanPath, []byte(orphanContent), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Remove the orphaned snippet by domain
	if err := mgr.RemoveDomainSnippet("orphan.example.com"); err != nil {
		t.Fatalf("RemoveDomainSnippet orphan: %v", err)
	}

	// Verify removed
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Error("expected orphaned snippet file to be removed")
	}
}

// TestCaddyfilePath tests that the main Caddyfile content is correct.
func TestCaddyfilePath(t *testing.T) {
	startContent := MainCaddyfile()
	if !strings.Contains(startContent, "admin off") {
		t.Error("expected admin off in main caddyfile")
	}
	if !strings.Contains(startContent, "import sites/*.conf") {
		t.Error("expected import sites/*.conf")
	}
}

// TestSiteBlockFormat tests exact SiteBlock format for public and localhost.
func TestSiteBlockFormat(t *testing.T) {
	// Public domain
	block := SiteBlock("app.example.com", 8080)
	want := "app.example.com {\n    reverse_proxy localhost:8080\n}\n"
	if block != want {
		t.Errorf("public site block mismatch:\ngot:  %q\nwant: %q", block, want)
	}

	// Localhost domain
	block = SiteBlock("dev.localhost", 3000)
	want = "dev.localhost {\n    tls internal\n    reverse_proxy localhost:3000\n}\n"
	if block != want {
		t.Errorf("localhost site block mismatch:\ngot:  %q\nwant: %q", block, want)
	}
}

// TestSiteFilenameDeterministic tests that SiteFilename is deterministic.
func TestSiteFilenameDeterministic(t *testing.T) {
	a := SiteFilename("myapp", "example.com")
	b := SiteFilename("myapp", "example.com")
	if a != b {
		t.Error("expected SiteFilename to be deterministic")
	}
}

// ---------------------------------------------------------------------------
// Crash detection and auto-restart tests
// ---------------------------------------------------------------------------

// TestCrashDetectionAndRestart verifies that a crashed caddy process is
// automatically restarted by the crash watcher.
func TestCrashDetectionAndRestart(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	// Fake caddy that crashes on first run but stays up on subsequent runs.
	// Uses a marker file to detect first vs subsequent runs.
	markerFile := filepath.Join(tmpDir, "caddy-crash-restart-marker")
	fakeCaddy := filepath.Join(tmpDir, "fake-caddy-crash-restart.sh")
	script := fmt.Sprintf(`#!/bin/sh
if [ -f "%s" ]; then
    # Already crashed once — stay alive
    while [ 1 ]; do sleep 1; done
fi
# First run — crash immediately, create marker
touch "%s"
exit 1
`, markerFile, markerFile)
	if err := os.WriteFile(fakeCaddy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile fake caddy: %v", err)
	}

	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin(fakeCaddy)
	// Fast backoff for testing
	mgr.RestartMaxAttempts = 3
	mgr.RestartBackoff = 10 * time.Millisecond
	mgr.RestartMaxBackoff = 50 * time.Millisecond

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.Stop()

	// Process should crash (first run exits with 1) and be restarted.
	// Backoff: 10ms first attempt, so within ~1s the second instance should be up.
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		if mgr.IsRunning() {
			return // restarted successfully
		}
	}
	t.Fatal("expected Caddy to be restarted after crash")
}

// TestBackoffMaxAttempts verifies that the restart loop gives up after
// RestartMaxAttempts attempts when the process keeps crashing.
func TestBackoffMaxAttempts(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	// Fake caddy that always crashes
	fakeCaddy := filepath.Join(tmpDir, "fake-caddy-always-crash.sh")
	script := `#!/bin/sh
echo "Caddy crashing on purpose"
sleep 0.3
exit 1
`
	if err := os.WriteFile(fakeCaddy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile fake caddy: %v", err)
	}

	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin(fakeCaddy)
	// Small values for fast test
	mgr.RestartMaxAttempts = 2
	mgr.RestartBackoff = 5 * time.Millisecond
	mgr.RestartMaxBackoff = 20 * time.Millisecond

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Each attempt takes ~100ms (startProcess sleep) + backoff.
	// With 2 attempts: ~100ms + 5ms + 100ms ≈ 205ms.
	// Wait generously.
	time.Sleep(1500 * time.Millisecond)

	if mgr.IsRunning() {
		t.Fatal("expected Caddy to NOT be running after exhausting restart attempts")
	}
}

// TestWaitForReady verifies that WaitForReady returns when the process is running.
func TestWaitForReady(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	fakeCaddy := filepath.Join(tmpDir, "fake-caddy-ready.sh")
	script := `#!/bin/sh
echo "Caddy starting"
while [ 1 ]; do sleep 1; done
`
	if err := os.WriteFile(fakeCaddy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile fake caddy: %v", err)
	}

	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin(fakeCaddy)

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.Stop()

	if err := mgr.WaitForReady(3 * time.Second); err != nil {
		t.Fatalf("WaitForReady: %v", err)
	}
}

// TestWaitForReadyTimeout verifies that WaitForReady returns an error when
// the process never becomes ready.
func TestWaitForReadyTimeout(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	// Process that never starts — use a nonexistent binary
	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin("/nonexistent/binary-that-never-exists-xxxx")

	// Start will fail, so WaitForReady should timeout
	err := mgr.Start()
	if err == nil {
		t.Fatal("expected Start to fail with missing binary")
	}

	// WaitForReady on a stopped manager should timeout
	err = mgr.WaitForReady(200 * time.Millisecond)
	if err == nil {
		t.Fatal("expected WaitForReady to timeout")
	}
}

// TestStopPreventsRestartLoop verifies that after Stop(), a crashed process
// does not trigger a restart loop.
func TestStopPreventsRestartLoop(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	// Fake caddy that stays up until killed
	fakeCaddy := filepath.Join(tmpDir, "fake-caddy-stop-prevent.sh")
	script := `#!/bin/sh
echo "Caddy starting"
while [ 1 ]; do sleep 1; done
`
	if err := os.WriteFile(fakeCaddy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile fake caddy: %v", err)
	}

	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin(fakeCaddy)
	mgr.RestartMaxAttempts = 3
	mgr.RestartBackoff = 10 * time.Millisecond
	mgr.RestartMaxBackoff = 50 * time.Millisecond

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify it's running
	if !mgr.IsRunning() {
		t.Fatal("expected Caddy to be running after Start")
	}

	// Stop properly
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Wait a generous amount of time — if the restart loop were active,
	// it would have started a new process by now.
	time.Sleep(500 * time.Millisecond)

	if mgr.IsRunning() {
		t.Fatal("expected Caddy to remain stopped after Stop()")
	}
}

// TestStopDuringCrashRestart verifies that Stop() interrupts an in-progress
// restart loop and prevents further restart attempts.
func TestStopDuringCrashRestart(t *testing.T) {
	db := setupTestDB(t)
	tmpDir := t.TempDir()

	// Fake caddy that always crashes immediately
	fakeCaddy := filepath.Join(tmpDir, "fake-caddy-stop-during.sh")
	script := `#!/bin/sh
echo "Caddy crashing"
exit 1
`
	if err := os.WriteFile(fakeCaddy, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile fake caddy: %v", err)
	}

	mgr := NewCaddyManager(db, tmpDir)
	mgr.SetCaddyBin(fakeCaddy)
	// Use a longer backoff so we have time to call Stop() during the restart delay
	mgr.RestartMaxAttempts = 5
	mgr.RestartBackoff = 500 * time.Millisecond
	mgr.RestartMaxBackoff = 1 * time.Second

	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for first crash to be detected and restart loop to start
	time.Sleep(300 * time.Millisecond)

	// Call Stop() while restart loop is sleeping (between attempts)
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Wait longer than the remaining backoff — if Stop() didn't prevent
	// the loop, we'd see a new process start.
	time.Sleep(1500 * time.Millisecond)

	if mgr.IsRunning() {
		t.Fatal("expected Caddy to remain stopped after Stop() during crash loop")
	}
}

// TestGenerateAppSnippetWithCerts tests that public domains get an http://
// listener plus a tls line pointing at the origin cert pair when both cert
// files exist, while localhost domains keep tls internal without the http://
// prefix.
func TestGenerateAppSnippetWithCerts(t *testing.T) {
	tmpDir := t.TempDir()

	certDir := filepath.Join(tmpDir, "certs")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	certPath := filepath.Join(certDir, "origin.pem")
	keyPath := filepath.Join(certDir, "origin.key")
	if err := os.WriteFile(certPath, []byte("cert"), 0600); err != nil {
		t.Fatalf("WriteFile cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}

	mgr := NewCaddyManager(nil, tmpDir)
	snippet := mgr.generateAppSnippet("myapp", 12345, []string{"example.com", "dev.localhost"}, nil)

	want := "# deploy: myapp\n" +
		"http://example.com {\n" +
		"    reverse_proxy localhost:12345\n" +
		"}\n" +
		"example.com {\n" +
		"    tls " + certPath + " " + keyPath + "\n" +
		"    reverse_proxy localhost:12345\n" +
		"}\n" +
		"dev.localhost {\n" +
		"    tls internal\n" +
		"    reverse_proxy localhost:12345\n" +
		"}\n"

	if snippet != want {
		t.Errorf("snippet mismatch:\ngot:\n%s\nwant:\n%s", snippet, want)
	}
}

// TestGenerateAppSnippetNoCerts tests that public domains omit the tls line
// when no origin cert files exist, but still get the dual http/https listener.
func TestGenerateAppSnippetNoCerts(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewCaddyManager(nil, tmpDir)

	snippet := mgr.generateAppSnippet("myapp", 12345, []string{"example.com"}, nil)

	want := "# deploy: myapp\n" +
		"http://example.com {\n" +
		"    reverse_proxy localhost:12345\n" +
		"}\n" +
		"example.com {\n" +
		"    reverse_proxy localhost:12345\n" +
		"}\n"

	if snippet != want {
		t.Errorf("snippet mismatch:\ngot:\n%s\nwant:\n%s", snippet, want)
	}
}


// TestGenerateAppSnippetHTTPOnly tests that an http-only domain emits ONLY the
// http:// listener block — no TLS/https block — even when origin certs exist.
func TestGenerateAppSnippetHTTPOnly(t *testing.T) {
	tmpDir := t.TempDir()

	certDir := filepath.Join(tmpDir, "certs")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	certPath := filepath.Join(certDir, "origin.pem")
	keyPath := filepath.Join(certDir, "origin.key")
	if err := os.WriteFile(certPath, []byte("cert"), 0600); err != nil {
		t.Fatalf("WriteFile cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}

	mgr := NewCaddyManager(nil, tmpDir)
	snippet := mgr.generateAppSnippet("myapp", 12345, []string{"example.com"}, map[string]bool{"example.com": true})

	want := "# deploy: myapp\n" +
		"http://example.com {\n" +
		"    reverse_proxy localhost:12345\n" +
		"}\n"

	if snippet != want {
		t.Errorf("snippet mismatch:\ngot:\n%s\nwant:\n%s", snippet, want)
	}
	if strings.Contains(snippet, "tls ") {
		t.Errorf("expected no tls line in http-only snippet, got:\n%s", snippet)
	}
	// Only the http:// block may exist — no bare (https) block for example.com.
	if strings.Contains(snippet, "\nexample.com {") {
		t.Errorf("expected no bare https block in http-only snippet, got:\n%s", snippet)
	}
}
