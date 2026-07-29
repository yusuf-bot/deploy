package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var uninstallForce bool

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove deploy and all its data",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !uninstallForce {
			fmt.Println("")
			fmt.Println("⚠️  WARNING: This will REMOVE deploy, its daemon, and ALL data.")
			fmt.Println("This includes all apps, containers, images, secrets, and configuration.")
			fmt.Println("This action CANNOT be undone.")
			fmt.Println("")
			fmt.Print("To confirm, type DELETE: ")
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "DELETE" {
				return fmt.Errorf("cancelled")
			}
		}

		// 1. Stop daemon via API (best effort)
		c := newClient()
		_ = c.SetConfig("_shutdown", "true") // best effort — may fail if daemon not running

		// 2. Stop systemd service if present
		exec.Command("sudo", "systemctl", "stop", "deploy").Run()
		exec.Command("sudo", "systemctl", "disable", "deploy").Run()

		// 3. Remove systemd service file
		systemdPaths := []string{
			"/etc/systemd/system/deploy.service",
			"/usr/lib/systemd/system/deploy.service",
		}
		for _, p := range systemdPaths {
			os.Remove(p)
		}

		// 4. Reload systemd
		exec.Command("sudo", "systemctl", "daemon-reload").Run()

		// 5. Remove socket
		socketPath := config.SocketPath()
		os.Remove(socketPath)

		// 6. Remove data directory
		deployDir := config.DeployDirPath()
		fmt.Printf("Removing %s...\n", deployDir)
		if err := os.RemoveAll(deployDir); err != nil {
			return fmt.Errorf("remove data dir: %w", err)
		}

		// 7. Remove binary
		binaryPath, _ := os.Executable()
		if binaryPath != "" {
			fmt.Printf("Removing %s...\n", binaryPath)
			if err := os.Remove(binaryPath); err != nil {
				fmt.Printf("warning: could not remove binary: %v\n", err)
				fmt.Println("Please remove it manually: sudo rm", binaryPath)
			}
		}

		fmt.Println("deploy uninstalled successfully.")
		fmt.Println("Some files may remain if created by Docker (containers, images, volumes).")
		fmt.Println("Clean them with: docker system prune -a")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
	uninstallCmd.Flags().BoolVarP(&uninstallForce, "force", "f", false, "Skip confirmation")
}
