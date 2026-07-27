package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var usageCmd = &cobra.Command{
	Use:   "usage <app-name>",
	Short: "Show resource usage for a running container",
	Long: `Display live resource usage (CPU, memory, network I/O) for a
running container via docker stats.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		c := newClient()

		app, err := c.GetApp(name)
		if err != nil {
			return fmt.Errorf("get app: %w", err)
		}

		if app.ContainerID == "" {
			return fmt.Errorf("app %q has no container", name)
		}
		if app.Status != "running" {
			return fmt.Errorf("app %q is not running (status: %s)", name, app.Status)
		}

		docker := exec.Command("docker", "stats", app.ContainerID, "--no-stream")
		docker.Stdout = os.Stdout
		docker.Stderr = os.Stderr
		if err := docker.Run(); err != nil {
			return fmt.Errorf("docker stats: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(usageCmd)
}
