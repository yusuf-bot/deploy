package cmd

import (
	"context"
	"fmt"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"deploy/internal/api"
	"deploy/internal/audit"
	"deploy/internal/caddyfile"
	"deploy/internal/config"
	"deploy/internal/deploy"
	"deploy/internal/logging"
	"deploy/internal/types"
	"deploy/internal/runner"
	"deploy/internal/scheduler"
	"deploy/internal/state"

	"github.com/spf13/cobra"
	"github.com/moby/moby/client"
)

var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Start the deploy daemon (for systemd or direct use)",
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemon()
	},
}

func runDaemon() error {
	// Structured JSON logging can be forced via env; otherwise it falls back
	// to the log_format setting read after the DB is migrated below.
	logFormatEnv := os.Getenv("DEPLOY_LOG_FORMAT")
	if logFormatEnv != "" {
		logging.Setup(logFormatEnv)
	}

	// If running as root via sudo, switch to the real user's home
	if os.Geteuid() == 0 {
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
			usr, err := user.Lookup(sudoUser)
			if err == nil {
				os.Setenv("DEPLOY_HOME", filepath.Join(usr.HomeDir, config.DeployDir))
			}
		}
	}

	// Initialize directories
	if err := config.InitDir(); err != nil {
		return fmt.Errorf("init dir: %w", err)
	}
	// Socket is now at /var/run/deploy/deploy.sock
	// Created by the daemon's ListenAndServe via os.MkdirAll

	// Load or generate master key for secret encryption
	masterKey, err := state.EnsureMasterKey(config.DeployDirPath())
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}

	// Open database
	db, err := state.OpenDB(config.DBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// Migrate
	if err := state.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// If not forced via env, honor the log_format setting (json|text).
	if logFormatEnv == "" {
		if v, err := state.GetSetting(db, "log_format"); err != nil {
			log.Printf("warning: get log_format setting: %v", err)
		} else if v != "" {
			logging.Setup(v)
		}
	}

	// Create Docker runner
	dockerRunner, err := runner.NewDockerRunner()
	if err != nil {
		return fmt.Errorf("create docker runner: %w", err)
	}

	// Create Docker SDK client for low-level operations
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}

	// Create scheduler
	sched := scheduler.New(db)
	defer sched.Stop()

	// Create lock manager
	lockManager := deploy.NewLockManager()

	// Create deployer
	deployer := deploy.NewDeployer(dockerRunner, dockerClient, db, lockManager, masterKey)

	// Create Caddy manager
	caddyDir := config.CaddyDir()
	cm := caddyfile.NewCaddyManager(db, caddyDir)
	if err := cm.Start(); err != nil {
		log.Printf("warning: caddy not available: %v (domains disabled)", err)
		// Non-fatal — domains work when caddy is available later
	} else {
		deployer.SetCaddyManager(cm)
		// Wait for Caddy to be ready before accepting deploys
		if err := cm.WaitForReady(5 * time.Second); err != nil {
			log.Printf("warning: caddy not ready: %v", err)
		}
	}
	defer func() {
		if cm.IsRunning() {
			cm.Stop()
		}
	}()

	// Reconcile app state with Docker containers
	if err := reconcileAppState(db, dockerRunner); err != nil {
		return fmt.Errorf("reconcile app state: %w", err)
	}

	// Auto-start apps that should be running
	v, err := state.GetSetting(db, "auto_start")
	if err != nil {
		log.Printf("warning: get auto_start setting: %v", err)
	}
	if v == "true" {
		reconciledApps, _ := state.ListApps(db, "")
		ctx := context.Background()
		for _, app := range reconciledApps {
			if app.Status != types.StatusCreated && app.Status != types.StatusRunning {
				continue
			}
			if app.ContainerID != "" {
				continue
			}
			log.Printf("auto-starting app %q...", app.Name)

			// Inject decrypted secrets (they override deploy.yml env on
			// conflict), matching promote/rollback behavior.
			secrets, err := state.ListSecretsByApp(db, app.ID, masterKey)
			if err != nil {
				log.Printf("warning: auto-start %q: list secrets: %v", app.Name, err)
				secrets = nil
			}
			// Get group env if app belongs to a group
			var groupEnv map[string]string
			if app.GroupID != nil {
				groupEnv, _ = state.GetGroupEnv(db, *app.GroupID)
			}
			if len(secrets) > 0 || len(groupEnv) > 0 {
				app.Env = state.MergeEnvMap(app.Env, groupEnv, secrets)
			}

			ver := fmt.Sprintf("auto-%d", time.Now().Unix())
			containerID, err := dockerRunner.CreateContainer(ctx, &app, ver)
			if err != nil {
				log.Printf("auto-start %q: create container: %v", app.Name, err)
				continue
			}
			if err := dockerRunner.StartContainer(ctx, containerID); err != nil {
				dockerRunner.RemoveContainer(ctx, containerID)
				log.Printf("auto-start %q: start container: %v", app.Name, err)
				continue
			}
			if err := state.UpdateAppContainer(db, app.Name, containerID); err != nil {
				log.Printf("auto-start %s: update container ID: %v", app.Name, err)
				continue
			}
			if err := state.UpdateAppStatus(db, app.Name, types.StatusRunning); err != nil {
				log.Printf("auto-start %s: update status: %v", app.Name, err)
				continue
			}
			log.Printf("auto-started %q (container=%s)", app.Name, containerShortID(containerID))
		}
	}

	// Health check monitor — runs every 30s for all running apps.
	healthCtx, cancelHealth := context.WithCancel(context.Background())
	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		runHealthMonitor(healthCtx, db, dockerRunner)
	}()
	defer func() {
		cancelHealth()
		select {
		case <-healthDone:
		case <-time.After(5 * time.Second):
		}
	}()

	// Audit log retention prune — runs every 24h, honoring the
	// audit_retention_days setting (0 = keep forever).
	auditPruneCtx, cancelAuditPrune := context.WithCancel(context.Background())
	auditPruneDone := make(chan struct{})
	go func() {
		defer close(auditPruneDone)
		runAuditPrune(auditPruneCtx, db)
	}()
	defer func() {
		cancelAuditPrune()
		select {
		case <-auditPruneDone:
		case <-time.After(5 * time.Second):
		}
	}()

	server := api.NewServer(db, dockerRunner, sched, deployer, cm, config.SocketPath(), masterKey)

	// Scheduled per-app backups. Started only here (after server init), never
	// inside api.NewServer, so the hermetic test harness never sees it.
	backupCtx, cancelBackups := context.WithCancel(context.Background())
	backupsDone := make(chan struct{})
	go func() {
		defer close(backupsDone)
		runScheduledBackups(backupCtx, db)
	}()
	defer func() {
		cancelBackups()
		select {
		case <-backupsDone:
		case <-time.After(5 * time.Second):
		}
	}()

	// Channel to receive server errors
	errCh := make(chan error, 1)

	go func() {
		log.Printf("Deploy daemon v%s starting", config.Version)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or server error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	var sig os.Signal
	select {
	case sig = <-quit:
		log.Printf("received signal %v, shutting down...", sig)
	case err := <-errCh:
		return err
	}

	// Graceful shutdown: drain in-flight requests
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}

	// Stop Caddy
	if cm.IsRunning() {
		if err := cm.Stop(); err != nil {
			log.Printf("caddy stop error: %v", err)
		}
	}

	// sched.Stop() and db.Close() handled by defer
	log.Println("deploy daemon stopped gracefully")
	return nil
}


func containerShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// runAuditPrune prunes audit log entries older than the audit_retention_days
// setting (default 90; 0 = keep forever). It runs once at startup and then
// every 24h.
func runAuditPrune(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	runOnce := func() {
		days := 90
		v, err := state.GetSetting(db, "audit_retention_days")
		if err != nil || v == "" {
			days = 90
		} else if n, perr := strconv.Atoi(v); perr != nil {
			days = 90
		} else {
			days = n
		}
		if days <= 0 {
			return // keep forever
		}
		n, err := audit.PruneOlderThan(time.Duration(days) * 24 * time.Hour)
		if err != nil {
			log.Printf("audit prune: %v", err)
			return
		}
		if n > 0 {
			log.Printf("audit prune: removed %d entries older than %d days", n, days)
		}
	}

	runOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

// runScheduledBackups runs a per-app backup for every app when the
// backup_schedule setting matches the current time, then enforces
// backup_retention (keep the newest N archives per app). It checks once at
// startup and then every 60s, tracking the last-run minute so a scheduled
// backup never double-fires within the same minute.
func runScheduledBackups(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	var lastRun string // "2006-01-02 15:04" of the last scheduled backup

	runOnce := func(now time.Time) {
		sched, err := state.GetBackupSchedule(db)
		if err != nil {
			log.Printf("scheduled backup: read schedule: %v", err)
			return
		}
		if sched == nil || !sched.Match(now) {
			return
		}

		minuteKey := now.Format("2006-01-02 15:04")
		if lastRun == minuteKey {
			return
		}
		lastRun = minuteKey

		retention, err := state.GetBackupRetention(db)
		if err != nil {
			log.Printf("scheduled backup: read retention: %v", err)
			retention = state.DefaultBackupRetention
		}

		apps, err := state.ListApps(db, "")
		if err != nil {
			log.Printf("scheduled backup: list apps: %v", err)
			return
		}
		for _, app := range apps {
			path, err := api.CreateAppBackup(db, app.Name)
			if err != nil {
				log.Printf("scheduled backup %q: %v", app.Name, err)
				continue
			}
			log.Printf("scheduled backup %q: %s", app.Name, path)
		}

		removed, err := api.PruneAppBackups(filepath.Join(config.DeployDirPath(), "backups"), retention)
		if err != nil {
			log.Printf("scheduled backup: prune: %v", err)
			return
		}
		for _, p := range removed {
			log.Printf("scheduled backup: pruned %s", p)
		}
	}

	runOnce(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			runOnce(now)
		}
	}
}

const (
	healthCheckInterval = 30 * time.Second
	healthWebhookCooldown = 5 * time.Minute
)

// runHealthMonitor checks all running apps' health every 30s and sends
// webhook alerts when an app transitions from ok to failed, with a 5-minute
// cooldown between notifications per app.
func runHealthMonitor(ctx context.Context, db *sql.DB, dockerRunner runner.Interface) {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	// Track last notification time per app to enforce cooldown
	lastNotified := make(map[string]time.Time)

	runOnce := func(now time.Time) {
		apps, err := state.ListApps(db, "")
		if err != nil {
			log.Printf("health monitor: list apps: %v", err)
			return
		}

		webhookURL, _ := state.GetSetting(db, "webhook_url")
		webhookSecret, _ := state.GetSetting(db, "webhook_secret")

		for _, app := range apps {
			if app.Status != types.StatusRunning || app.ContainerID == "" {
				continue
			}
			if app.HealthPath == "" {
				continue
			}

			healthPath := app.HealthPath
			if !strings.HasPrefix(healthPath, "/") {
				healthPath = "/" + healthPath
			}

			port := app.ServicePort
			if port == 0 {
				port = app.Port
			}

			checkURL := fmt.Sprintf("http://127.0.0.1:%d%s", port, healthPath)
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(checkURL)

			currentStatus := "ok"
			var lastErr string
			if err != nil {
				currentStatus = "failed"
				lastErr = err.Error()
			} else {
				resp.Body.Close()
				if resp.StatusCode >= 500 {
					currentStatus = "failed"
					lastErr = fmt.Sprintf("HTTP %d", resp.StatusCode)
				}
			}

			// Get previous status
			health, err := state.GetAppHealth(db, app.ID)
			if err != nil {
				log.Printf("health monitor: get health %q: %v", app.Name, err)
				continue
			}
			prevStatus := health.Status

			// Update health record
			lastChecked := now.Format(time.RFC3339)
			lastOk := health.LastOk
			lastNotifiedStr := health.LastNotified
			if currentStatus == "ok" {
				lastOk = lastChecked
			}
			if err := state.UpdateAppHealth(db, app.ID, currentStatus, lastChecked, lastOk, lastErr, lastNotifiedStr); err != nil {
				log.Printf("health monitor: update health %q: %v", app.Name, err)
				continue
			}

			// Send webhook on ok->failed transition (with cooldown)
			if prevStatus == "ok" && currentStatus == "failed" && webhookURL != "" {
				if last, ok := lastNotified[app.Name]; ok && now.Sub(last) < healthWebhookCooldown {
					log.Printf("health monitor: %q failed but cooldown active, skipping webhook", app.Name)
					continue
				}
				lastNotified[app.Name] = now
				go sendHealthWebhook(webhookURL, webhookSecret, app.Name, lastErr)
			}

			if prevStatus != currentStatus {
				log.Printf("health monitor: %q status %s -> %s", app.Name, prevStatus, currentStatus)
			}
		}
	}

	runOnce(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			runOnce(now)
		}
	}
}

// sendHealthWebhook sends an alert to the configured webhook URL.
func sendHealthWebhook(webhookURL, secret, appName, errMsg string) {
	payload := fmt.Sprintf(`{"event":"health_failed","app":"%s","error":"%s","ts":"%s"}`,
		appName, errMsg, time.Now().UTC().Format(time.RFC3339))

	req, err := http.NewRequest("POST", webhookURL, strings.NewReader(payload))
	if err != nil {
		log.Printf("health webhook: create request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Deploy-Secret", secret)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		log.Printf("health webhook: send: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("health webhook: status %d", resp.StatusCode)
	}
}


// reconcileAppState checks each app's container status in Docker and updates
// the database to match. Apps with running containers stay running, apps
// without containers get marked as stopped.
func reconcileAppState(db *sql.DB, runner runner.Interface) error {
	apps, err := state.ListApps(db, "")
	if err != nil {
		return fmt.Errorf("list apps: %w", err)
	}

	for _, app := range apps {
		containerID, err := runner.FindContainerByLabel(context.Background(), "deploy.app.name", app.Name)
		if err == nil && containerID != "" {
			// Container exists — set status to running and update container ID
			if app.Status != types.StatusRunning {
				if err := state.UpdateAppStatus(db, app.Name, types.StatusRunning); err != nil {
					log.Printf("reconcile %s: update status: %v", app.Name, err)
				}
			}
			if app.ContainerID != containerID {
				if err := state.UpdateAppContainer(db, app.Name, containerID); err != nil {
					log.Printf("reconcile %s: update container ID: %v", app.Name, err)
				}
			}
			continue
		}

		// Container doesn't exist in Docker
		if app.Status == types.StatusRunning {
			log.Printf("app %s was running but container is gone, marking as stopped", app.Name)
			if err := state.UpdateAppStatus(db, app.Name, types.StatusStopped); err != nil {
				log.Printf("reconcile %s: update status: %v", app.Name, err)
			}
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(daemonCmd)
}