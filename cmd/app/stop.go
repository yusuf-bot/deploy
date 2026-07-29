package app

import (
	"encoding/json"
	"fmt"

	"deploy/internal/client"
	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Stop an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		async := isAsyncFlag(cmd)

		c := client.New(config.SocketPath())
		resp, err := c.StopApp(name, async)
		if err != nil {
			return fmt.Errorf("stop app: %w", err)
		}

		if isJSONFlag(cmd) {
			data, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if resp.JobID != "" {
			fmt.Printf("Stopped async job %s for app %q\n", resp.JobID, name)
		} else {
			fmt.Printf("App %q stopped\n", name)
		}
		return nil
	},
}

func init() {
}
