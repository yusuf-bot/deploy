package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func runDockerExec(containerID, shell string) error {
	stat, _ := os.Stdin.Stat()
	tty := (stat.Mode() & os.ModeCharDevice) != 0

	args := []string{"exec"}
	if tty {
		args = append(args, "-it")
	} else {
		args = append(args, "-i")
	}
	args = append(args, containerID, shell)

	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	// Register signal forwarding AFTER Start (Process is guaranteed non-nil)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		cmd.Process.Signal(sig)
	}()

	err := cmd.Wait()
	signal.Stop(sigChan)
	close(sigChan)
	return err
}

var sshCmd = &cobra.Command{
	Use:   "ssh <app-name>",
	Short: "Open a shell in the running container",
	Long: `Open an interactive shell (bash, falling back to sh) inside a
running container via Docker exec.`,
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

		err = runDockerExec(app.ContainerID, "bash")
		if err != nil {
			// If bash not found (exit 126 or 127), fall back to sh
			if exitErr, ok := err.(*exec.ExitError); ok {
				code := exitErr.ExitCode()
				if code == 126 || code == 127 {
					return runDockerExec(app.ContainerID, "sh")
				}
			}
			return fmt.Errorf("ssh: %w", err)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(sshCmd)
}
