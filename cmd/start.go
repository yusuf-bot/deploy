package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start <app-name>",
	Short: "Start an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		c := newClient()
		resp, err := c.StartApp(name, asyncFlag)
		if err != nil {
			return fmt.Errorf("start app: %w", err)
		}

		if jsonFlag {
			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
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
	rootCmd.AddCommand(startCmd)
}
