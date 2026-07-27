package app

import (
	"bufio"
	"fmt"
	"io"

	"deploy/internal/client"
	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var (
	tailLines int
	followLog bool
)

var logsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Show application logs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		c := client.New(config.SocketPath())
		reader, err := c.GetLogs(name, tailLines, followLog)
		if err != nil {
			return fmt.Errorf("get logs: %w", err)
		}
		defer reader.Close()

		if isJSONFlag(cmd) && !followLog {
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
	logsCmd.Flags().IntVar(&tailLines, "tail", 100, "Number of lines to show from the end")
	logsCmd.Flags().BoolVar(&followLog, "follow", false, "Follow log output")
}
