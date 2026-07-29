package caddyfile

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"sync"
	"bytes"
	"syscall"
	"time"

	"deploy/internal/state"
)

// SiteDir is the sites subdirectory name.
const SiteDir = "sites"

// DefaultCaddyBin is the default caddy binary name looked up in PATH.
const DefaultCaddyBin = "caddy"

// StopTimeout is how long to wait for Caddy to stop gracefully before SIGKILL.
const StopTimeout = 10 * time.Second

// Default restart configuration.
const (
	defaultRestartMaxAttempts = 5
	defaultRestartBackoff     = 1 * time.Second
	defaultRestartMaxBackoff  = 30 * time.Second
)

// CaddyManager manages a Caddy subprocess and its configuration files.
//
// Caddy runs as a subprocess (not embedded). The daemon writes snippet files
// to configDir/sites/*.conf, and the main Caddyfile imports them via a glob
// pattern. On changes, the daemon updates the snippets and sends SIGHUP to
// trigger a config reload.
//
// Crash detection: a watcher goroutine waits for cmd.Wait() and restarts
// the process with exponential backoff on unexpected exits. Stop() closes
// stopCh to prevent the restart loop during intentional shutdown.
type CaddyManager struct {
	db        *sql.DB
	configDir string     // ~/.deploy/caddy
	caddyBin  string     // path to caddy binary (default "caddy")
	cmd       *exec.Cmd  // running caddy process
	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc

	// Crash detection and restart
	stopCh   chan struct{} // closed by Stop() to signal watcher to not restart
	stopOnce sync.Once     // ensures stopCh is closed at most once
	exitedCh chan struct{} // closed by watcher when cmd.Wait() returns (recreated per start)

	// Restart configuration (exported for testing)
	// Total restart attempts across crash cycles
	restartAttempts int
	RestartMaxAttempts int
	RestartBackoff     time.Duration
	RestartMaxBackoff  time.Duration
}

// NewCaddyManager creates a new CaddyManager.
func NewCaddyManager(db *sql.DB, configDir string) *CaddyManager {
	return &CaddyManager{
		db:        db,
		configDir: configDir,
		caddyBin:  DefaultCaddyBin,
		stopCh:    make(chan struct{}),

		RestartMaxAttempts: defaultRestartMaxAttempts,
		RestartBackoff:     defaultRestartBackoff,
		RestartMaxBackoff:  defaultRestartMaxBackoff,
	}
}

// SetCaddyBin sets the path to the caddy binary. Useful for testing.
func (m *CaddyManager) SetCaddyBin(path string) {
	m.caddyBin = path
}

// findBinary locates the caddy binary, checking in order:
//  1. The configured absolute path (m.caddyBin if absolute)
//  2. PATH lookup of m.caddyBin
//  3. ~/.deploy/caddy/caddy (downloaded by deploy init)
//
// Returns empty string if not found.
func (m *CaddyManager) findBinary() string {
	// 1. Check configured absolute path
	if m.caddyBin != "" && filepath.IsAbs(m.caddyBin) {
		if _, err := os.Stat(m.caddyBin); err == nil {
			return m.caddyBin
		}
		return ""
	}

	// 2. Check PATH (m.caddyBin is "caddy" by default)
	if m.caddyBin != "" {
		if path, err := exec.LookPath(m.caddyBin); err == nil {
			return path
		}
	}

	// 3. Check ~/.deploy/caddy/caddy (deploy init downloads here)
	candidate := filepath.Join(m.configDir, "caddy")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	return ""
}

// sitesDir returns the path to the sites subdirectory.
func (m *CaddyManager) sitesDir() string {
	return filepath.Join(m.configDir, SiteDir)
}

// caddyfilePath returns the path to the main Caddyfile.
func (m *CaddyManager) caddyfilePath() string {
	return filepath.Join(m.configDir, "Caddyfile")
}

// Start starts the Caddy subprocess.
//
//  1. Check caddy binary exists.
//  2. Ensure configDir + sites/ subdirectory exist.
//  3. Write main Caddyfile.
//  4. Generate site blocks from current DB state.
//  5. Start caddy run.
//  6. Verify process started.
//  7. Launch crash watcher goroutine.
func (m *CaddyManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("caddy is already running")
	}

	// Reset restart counter on fresh start
	m.restartAttempts = 0

	if err := m.startProcess(); err != nil {
		return err
	}

	log.Printf("Caddy started (pid %d)", m.cmd.Process.Pid)
	return nil
}

// startProcess handles the process creation and verification.
// It finds the binary, writes config, starts caddy, and verifies the process
// is alive. The crash watcher goroutine is launched before returning.
// Caller must hold m.mu.
func (m *CaddyManager) startProcess() error {
	// 1. Check caddy binary exists
	caddyPath := m.findBinary()
	if caddyPath == "" {
		return fmt.Errorf("caddy binary not found (checked PATH and %s)", filepath.Join(m.configDir, "caddy"))
	}

	// 2. Ensure directories exist
	if err := os.MkdirAll(m.sitesDir(), 0700); err != nil {
		return fmt.Errorf("create sites dir: %w", err)
	}

	// 3. Write main Caddyfile
	if err := os.WriteFile(m.caddyfilePath(), []byte(MainCaddyfile()), 0600); err != nil {
		return fmt.Errorf("write main Caddyfile: %w", err)
	}

	// 4. Generate site snippets from DB state
	if err := m.generateSnippets(); err != nil {
		return fmt.Errorf("generate snippets: %w", err)
	}

	// 5. Start caddy
	// Cancel any previous context before creating a new one
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, caddyPath, "run", "--config", m.caddyfilePath())
	// LD_PRELOAD may be set by torsocks which interferes with caddy networking
	cmd.Env = append(os.Environ(), "LD_PRELOAD=")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start caddy: %w", err)
	}

	exitedCh := make(chan struct{})
	m.cmd = cmd
	m.running = true
	m.cancel = cancel
	m.exitedCh = exitedCh

	// 6. Verify process started (brief wait + check)
	// Verify process started (goroutine + channel)
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	select {
	case err := <-waitCh:
		m.running = false
		m.cancel = nil
		m.cmd = nil
		m.exitedCh = nil
		cancel()
		close(exitedCh)
		return fmt.Errorf("caddy exited immediately: %w", err)
	case <-time.After(100 * time.Millisecond):
		// Still alive
	}

	// 7. Launch crash watcher goroutine
	go m.watchProcess(exitedCh, cmd)

	return nil
}

// watchProcess waits for the caddy process to exit. If the exit is unexpected
// (not triggered by Stop()), it restarts the process with exponential backoff.
func (m *CaddyManager) watchProcess(exitedCh chan struct{}, cmd *exec.Cmd) {
	// Wait for process to exit
	err := cmd.Wait()

	// Signal that process has exited
	close(exitedCh)

	m.mu.Lock()
	m.exitedCh = nil
	if m.cmd == cmd {
		m.running = false
		m.cmd = nil
	}
	m.mu.Unlock()

	// Check if stop was requested — if so, don't restart
	select {
	case <-m.stopCh:
		return
	default:
	}

	if err != nil {
		log.Printf("caddy process exited: %v (restarting)", err)
	} else {
		log.Printf("caddy process exited (restarting)")
	}

	m.restartWithBackoff()
}

// restartWithBackoff attempts to restart caddy with exponential backoff.
// It gives up after RestartMaxAttempts attempts.
func (m *CaddyManager) restartWithBackoff() {
	backoff := m.RestartBackoff
	maxBackoff := m.RestartMaxBackoff
	maxAttempts := m.RestartMaxAttempts

	// Check total restart limit across crash cycles
	m.mu.Lock()
	if m.restartAttempts >= maxAttempts {
		m.mu.Unlock()
		log.Printf("caddy failed to restart after %d attempts, giving up", maxAttempts)
		return
	}
	m.restartAttempts++
	myAttempt := m.restartAttempts
	m.mu.Unlock()

	// Check if stop was requested before sleeping
	select {
	case <-m.stopCh:
		log.Printf("caddy restart cancelled (shutting down)")
		return
	default:
	}

	time.Sleep(backoff)

	// Check again after sleeping
	select {
	case <-m.stopCh:
		log.Printf("caddy restart cancelled (shutting down)")
		return
	default:
	}

	m.mu.Lock()
	err := m.startProcess()
	m.mu.Unlock()

	if err != nil {
		log.Printf("caddy restart attempt %d/%d failed: %v", myAttempt, maxAttempts, err)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		log.Printf("caddy failed to restart after %d attempts, giving up", maxAttempts)
		return
	}

	log.Printf("caddy restarted successfully (attempt %d/%d)", myAttempt, maxAttempts)
	return

	log.Printf("caddy failed to restart after %d attempts, giving up", maxAttempts)
}

// WaitForReady polls IsRunning until the process is alive or the timeout expires.
func (m *CaddyManager) WaitForReady(timeout time.Duration) error {
	deadline := time.After(timeout)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-deadline:
			return fmt.Errorf("caddy not ready within %v", timeout)
		case <-tick.C:
			if m.IsRunning() {
				return nil
			}
		}
	}
}

// Stop gracefully stops the Caddy subprocess.
//
// It signals the crash watcher to not restart (via stopCh), then sends SIGTERM
// and waits up to StopTimeout before sending SIGKILL. The crash watcher owns
// cmd.Wait(), so Stop() coordinates via the exitedCh channel instead.
func (m *CaddyManager) Stop() error {
	// Prevent restart loop: signal watcher(s) to not restart
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})

	m.mu.Lock()

	if !m.running || m.cmd == nil {
		m.mu.Unlock()
		return nil
	}

	proc := m.cmd.Process
	exitedCh := m.exitedCh

	if proc == nil {
		m.running = false
		m.cmd = nil
		m.mu.Unlock()
		return nil
	}

	// Send SIGTERM for graceful shutdown
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		m.running = false
		m.cmd = nil
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	// Wait for watcher to confirm exit (don't hold lock to avoid deadlock)
	if exitedCh != nil {
		select {
		case <-exitedCh:
			// Process exited cleanly
		case <-time.After(StopTimeout):
			// Timeout — force kill
			if err := proc.Kill(); err != nil {
				m.mu.Lock()
				m.running = false
				m.cmd = nil
				m.mu.Unlock()
				return fmt.Errorf("kill caddy after timeout: %w", err)
			}
			<-exitedCh
		}
	}

	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.running = false
	m.cmd = nil
	m.mu.Unlock()

	log.Printf("Caddy stopped")
	return nil
}

// Reload regenerates all site snippets from the current DB state and sends
// SIGHUP to Caddy to reload the configuration.
func (m *CaddyManager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.generateSnippets(); err != nil {
		return fmt.Errorf("regenerate snippets: %w", err)
	}

	if m.running && m.cmd != nil && m.cmd.Process != nil {
		return m.cmd.Process.Signal(syscall.SIGHUP)
	}

	return nil
}

// IsRunning checks if the Caddy process is still alive.
func (m *CaddyManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.cmd == nil || m.cmd.Process == nil {
		return false
	}

	alive := isProcessAlive(m.cmd.Process.Pid)
	if !alive {
		m.running = false
	}
	return alive
}

// AddDomainSnippet writes a site snippet file for the given domain and reloads Caddy.
func (m *CaddyManager) AddDomainSnippet(appName string, domain string, port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	snippet := generateAppSnippet(appName, port, []string{domain})
	filename := SiteFilename(appName, domain)
	path := filepath.Join(m.sitesDir(), filename)

	if err := os.MkdirAll(m.sitesDir(), 0700); err != nil {
		return fmt.Errorf("create sites dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(snippet), 0600); err != nil {
		return fmt.Errorf("write snippet %s: %w", filename, err)
	}

	if m.running && m.cmd != nil && m.cmd.Process != nil {
		return m.cmd.Process.Signal(syscall.SIGHUP)
	}

	return nil
}

// RemoveDomainSnippet finds and deletes the site file for a domain, then reloads Caddy.
func (m *CaddyManager) RemoveDomainSnippet(domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Look up the domain in the DB to find the app name
	dom, err := state.GetDomainByDomain(m.db, domain)
	if err != nil {
		return fmt.Errorf("lookup domain %s: %w", domain, err)
	}
	if dom == nil {
		// Domain not in DB — try to find and clean up orphaned files
		return m.removeOrphanedSnippet(domain)
	}

	app, err := state.GetApp(m.db, dom.AppID)
	if err != nil {
		return fmt.Errorf("lookup app for domain %s: %w", domain, err)
	}
	if app == nil {
		return fmt.Errorf("app not found for domain %s", domain)
	}

	filename := SiteFilename(app.Name, domain)
	path := filepath.Join(m.sitesDir(), filename)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove snippet %s: %w", filename, err)
	}

	if m.running && m.cmd != nil && m.cmd.Process != nil {
		return m.cmd.Process.Signal(syscall.SIGHUP)
	}

	return nil
}

// removeOrphanedSnippet attempts to delete any snippet file containing the given domain.
// Used when the domain is not in the DB (orphaned).
func (m *CaddyManager) removeOrphanedSnippet(domain string) error {
	entries, err := os.ReadDir(m.sitesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read sites dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		path := filepath.Join(m.sitesDir(), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), domain) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove orphaned snippet %s: %w", entry.Name(), err)
			}
			return nil
		}
	}

	return nil
}

// UpdatePortSnippets updates the port in all site snippets for the given app.
// Uses generateAppSnippet to regenerate each snippet atomically instead of
// string replacement, which could corrupt content.
func (m *CaddyManager) UpdatePortSnippets(appID string, oldPort int, newPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, err := state.GetApp(m.db, appID)
	if err != nil {
		return fmt.Errorf("lookup app %s: %w", appID, err)
	}
	if app == nil {
		return fmt.Errorf("app not found: %s", appID)
	}

	// Get all domains for this app from the DB
	domains, err := state.ListDomainsByApp(m.db, app.ID)
	if err != nil {
		return fmt.Errorf("list domains for app %s: %w", app.Name, err)
	}

	// Regenerate each domain snippet with the new port
	for _, d := range domains {
		snippet := generateAppSnippet(app.Name, newPort, []string{d.Domain})
		filename := SiteFilename(app.Name, d.Domain)
		path := filepath.Join(m.sitesDir(), filename)
		if err := os.MkdirAll(m.sitesDir(), 0700); err != nil {
			return fmt.Errorf("create sites dir: %w", err)
		}
		if err := os.WriteFile(path, []byte(snippet), 0600); err != nil {
			return fmt.Errorf("write snippet %s: %w", filename, err)
		}
	}

	if m.running && m.cmd != nil && m.cmd.Process != nil {
		return m.cmd.Process.Signal(syscall.SIGHUP)
	}

	return nil
}
// generateSnippets writes snippet files for all running apps with domains.
// generateSnippets writes snippet files for all running apps with domains.
// Old snippet files not in the current DB state are cleaned up.
func (m *CaddyManager) generateSnippets() error {
	apps, err := state.ListApps(m.db, "running")
	if err != nil {
		return fmt.Errorf("list running apps: %w", err)
	}

	// Build set of expected filenames
	expected := make(map[string]bool)

	for _, app := range apps {
		domains, err := state.ListDomainsByApp(m.db, app.ID)
		if err != nil {
			return fmt.Errorf("list domains for app %s: %w", app.Name, err)
		}

		for _, d := range domains {
			filename := SiteFilename(app.Name, d.Domain)
			expected[filename] = true

			path := filepath.Join(m.sitesDir(), filename)
			snippet := generateAppSnippet(app.Name, app.Port, []string{d.Domain})
			if err := os.MkdirAll(m.sitesDir(), 0700); err != nil {
				return fmt.Errorf("create sites dir: %w", err)
			}
			if err := os.WriteFile(path, []byte(snippet), 0600); err != nil {
				return fmt.Errorf("write snippet %s: %w", filename, err)
			}
		}
	}

	// Clean up old snippet files not in expected set
	entries, err := os.ReadDir(m.sitesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read sites dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		if !expected[entry.Name()] {
			path := filepath.Join(m.sitesDir(), entry.Name())
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale snippet %s: %w", entry.Name(), err)
			}
		}
	}

	return nil
}

// isProcessAlive checks whether a process with the given PID is still running.
func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
// appSnippetTemplate is the Go template for generating a Caddyfile snippet
// for an app with multiple domains.
const appSnippetTemplate = `# deploy: {{.AppName}}
{{range .Domains}}{{.}} {
    reverse_proxy localhost:{{.Port}}
}
{{end}}`

type appSnippetData struct {
	AppName string
	Port    int
	Domains []string
}

// generateAppSnippet generates a Caddyfile site snippet for an app's domains
// using a proper Go template. Replaces the old strings.ReplaceAll approach
// which could corrupt content.
func generateAppSnippet(appName string, port int, domains []string) string {
	tmpl := template.Must(template.New("app").Parse(appSnippetTemplate))
	var buf bytes.Buffer
	tmpl.Execute(&buf, appSnippetData{AppName: appName, Port: port, Domains: domains})
	return buf.String()
}
