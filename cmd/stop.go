package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop <app-name>",
	Short: "Stop an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		c := newClient()
		resp, err := c.StopApp(name, asyncFlag)
		if err != nil {
			return fmt.Errorf("stop app: %w", err)
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
			fmt.Printf("Stopped async job %s for app %q\n", resp.JobID, name)
		} else {
			fmt.Printf("App %q stopped\n", name)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}