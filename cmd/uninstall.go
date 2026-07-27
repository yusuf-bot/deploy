package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove deploy and all its data",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("This will remove deploy, its daemon, and all data.")
		fmt.Print("Are you sure? Type 'yes': ")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "yes" {
			return fmt.Errorf("cancelled")
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
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
