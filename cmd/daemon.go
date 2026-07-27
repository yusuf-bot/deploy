package cmd

import (
	"fmt"
	"context"
	"log"
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
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemon()
	},
}

func runDaemon() error {
	// Initialize directories
	if err := config.InitDir(); err != nil {
		return fmt.Errorf("init dir: %w", err)
	}
	if err := config.InitSocketDir(); err != nil {
		return fmt.Errorf("init socket dir: %w", err)
	}

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

	// Reset all running apps to unknown (no startup reconciliation)
	if err := state.SetAllRunningToUnknown(db); err != nil {
		return fmt.Errorf("reset running apps: %w", err)
	}

	// Create Docker runner
	dockerRunner, err := runner.NewDockerRunner()
	if err != nil {
		return fmt.Errorf("create docker runner: %w", err)
	}

	// Create Docker SDK client for low-level operations
	dockerClient, err := client.NewClientWithOpts(client.FromEnv)
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

	// Create API server with deployer and caddy manager
	// Auto-start apps if configured
	if v, _ := state.GetSetting(db, "auto_start"); v == "true" {
		apps, err := state.ListApps(db, types.StatusUnknown)
		if err == nil {
			for _, app := range apps {
				log.Printf("auto-starting app %q...", app.Name)
				ctx := context.Background()

				// Check if container already exists for this app label
				existingID, _ := dockerRunner.FindContainerByLabel(ctx, "deploy.app.name", app.Name)
				if existingID != "" {
					if err := state.UpdateAppContainer(db, app.Name, existingID); err != nil {
						log.Printf("auto-start %s: update container ID: %v", app.Name, err)
						continue
					}
					if err := state.UpdateAppStatus(db, app.Name, types.StatusRunning); err != nil {
						log.Printf("auto-start %s: update status: %v", app.Name, err)
						continue
					}
					log.Printf("auto-started %q (container=%s)", app.Name, containerShortID(existingID))
					continue
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
	}

	server := api.NewServer(db, dockerRunner, sched, deployer, cm, config.SocketPath(), masterKey)

	log.Printf("Deploy daemon v%s starting", config.Version)
	return server.ListenAndServe()
}


func containerShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func init() {
	rootCmd.AddCommand(daemonCmd)
}
