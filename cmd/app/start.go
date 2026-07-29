package app

import (
	"encoding/json"
	"fmt"

	"deploy/internal/client"
	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "Start an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		async := isAsyncFlag(cmd)

		c := client.New(config.SocketPath())
		resp, err := c.StartApp(name, async)
		if err != nil {
			return fmt.Errorf("start app: %w", err)
		}

		if isJSONFlag(cmd) {
			data, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if resp.JobID != "" {
			fmt.Printf("Started async job %s for app %q\n", resp.JobID, name)
		} else {
			fmt.Printf("App %q started (container: %s)\n", name, resp.Container)
		}
		if resp.Logs != "" {
			fmt.Printf("Logs:\n%s\n", resp.Logs)
		}
		return nil
	},
}

func init() {
}
