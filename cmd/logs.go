// Package cmd provides the CLI commands for deploy.
package cmd

import (
	"bufio"
	"fmt"
	"io"

	"deploy/internal/client"
	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var (
	logsTail   int
	logsFollow bool
)

// logsCmd represents the top-level "deploy logs" command for container logs.
var logsCmd = &cobra.Command{
	Use:   "logs <app-name>",
	Short: "Show container logs for an app",
	Long: `Display logs from a running container. Supports tailing and following.

By default outputs log lines as plain text. Use --json to get the raw
API response (array of log entries).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		c := client.New(config.SocketPath())

		reader, err := c.GetLogs(appName, logsTail, logsFollow)
		if err != nil {
			return fmt.Errorf("get logs: %w", err)
		}
		defer reader.Close()

		if jsonFlag && !logsFollow {
			data, err := io.ReadAll(reader)
			if err != nil {
				return fmt.Errorf("read logs: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
		return scanner.Err()
	},
}

func init() {
	logsCmd.Flags().IntVar(&logsTail, "tail", 100, "Number of lines to show from the end")
	logsCmd.Flags().BoolVar(&logsFollow, "follow", false, "Follow log output")
	rootCmd.AddCommand(logsCmd)
}
