package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"deploy/internal/client"
	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var (
	jsonFlag  bool
	waitFlag  = true // default wait=true; --wait flag is registered on `up` only
	asyncFlag bool
)
var Version = "0.3.0"

// rootCmd represents the base command.
var rootCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy — single-server personal PaaS",
	Long:  `Deploy is a single-server personal PaaS for managing Docker containers.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		skipCommands := map[string]bool{
			"init":       true,
			"daemon":     true,
			"help":       true,
			"completion": true,
			"version":    true,
		}
		if skipCommands[cmd.Name()] || cmd.Parent() == nil {
			return nil
		}

		socketPath := config.SocketPath()
		if _, err := os.Stat(socketPath); os.IsNotExist(err) {
			return fmt.Errorf("deploy daemon not running (socket %s not found); start with 'deploy daemon'", socketPath)
		}
		return nil
	},
}

// Execute adds all child commands to the root command.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		var ece *execExitError
		if errors.As(err, &ece) {
			os.Exit(ece.code)
		}
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&jsonFlag, "json", "j", false, "Output in JSON format")
	rootCmd.PersistentFlags().BoolVar(&asyncFlag, "async", false, "Run operation asynchronously")
	var versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(Version)
		},
	}
	rootCmd.AddCommand(versionCmd)
}

// newClient creates a deploy client connected to the daemon socket.
func newClient() *client.Client {
	return client.New(config.SocketPath())
}

// printError prints an error message.
func printError(code string, detail string) {
	if jsonFlag {
		fmt.Fprintf(os.Stderr, `{"error":"%s","code":"%s","detail":"%s"}`+"\n", code, code, detail)
	} else {
		fmt.Fprintf(os.Stderr, "ERROR [%s]: %s\n", code, detail)
	}
}

// fatalError prints an error and exits.
func fatalError(code string, detail string) {
	printError(code, detail)
	os.Exit(1)
}

// formatEnvVars formats env vars for display.
func formatEnvVars(env map[string]string) string {
	if len(env) == 0 {
		return "(none)"
	}
	pairs := make([]string, 0, len(env))
	for k, v := range env {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(pairs, ", ")
}
