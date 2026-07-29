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
	"syscall"
	"time"

	"deploy/internal/api"
	"deploy/internal/caddyfile"
	"deploy/internal/config"
	"deploy/internal/deploy"
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
	// If running as root via sudo, switch to the real user's home
	if os.Geteuid() == 0 {
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
			usr, err := user.Lookup(sudoUser)
			if err == nil {
				os.Setenv("DEPLOY_HOME", usr.HomeDir+"/.deploy")
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

	server := api.NewServer(db, dockerRunner, sched, deployer, cm, config.SocketPath(), masterKey)

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