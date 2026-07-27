package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart <app-name>",
	Short: "Restart an application (sequential stop + start)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		c := newClient()

		// Stop (wait for completion)
		stopResp, err := c.StopApp(name, true, false)
		if err != nil {
			return fmt.Errorf("restart: stop %q: %w", name, err)
		}

		// Start (wait for completion)
		startResp, err := c.StartApp(name, true, false)
		if err != nil {
			return fmt.Errorf("restart: start %q failed — app %q is STOPPED: %w", name, name, err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]interface{}{
				"stop":  stopResp,
				"start": startResp,
			}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("App %q restarted (container: %s)\n", name, startResp.Container)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(restartCmd)
}
