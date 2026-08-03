package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var execUserFlag string

// execExitError propagates the exit code of a container command so the
// deploy process exits with the same status.
type execExitError struct {
	code int
}

func (e *execExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}

var execCmd = &cobra.Command{
	Use:   "exec <app-name> -- <command> [args...]",
	Short: "Run a command in the app's running container",
	Long: `Run a non-interactive command inside the app's running container and
stream its output to the terminal. The command runs via docker exec and
deploy exits with the command's exit code.

Examples:
  deploy exec myapp -- ls -la
  deploy exec myapp -- sh -c 'echo $HOME'
  deploy exec -u root myapp -- id`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := execRun(args[0], args[1:])
		if err == nil {
			return nil
		}
		var ece *execExitError
		if !errors.As(err, &ece) {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		}
		return err
	},
}

// execRun streams a container command's output and returns its exit code as
// an execExitError when non-zero.
func execRun(appName string, cmdArgs []string) error {
	c := newClient()
	stream, err := c.Exec(appName, execUserFlag, cmdArgs)
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	defer stream.Output.Close()

	scanner := bufio.NewScanner(stream.Output)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read exec output: %w", err)
	}

	code, err := stream.ExitCode()
	if err != nil {
		return fmt.Errorf("get exec exit code: %w", err)
	}
	if code != 0 {
		return &execExitError{code: code}
	}
	return nil
}

func init() {
	execCmd.Flags().StringVarP(&execUserFlag, "user", "u", "", "run the command as this user (docker exec --user)")
	execCmd.SilenceErrors = true
	execCmd.SilenceUsage = true
	rootCmd.AddCommand(execCmd)
}
