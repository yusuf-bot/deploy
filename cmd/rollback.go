package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback <app-name> [version]",
	Short: "Rollback to a previous deployment",
	Long:  `Rollback an app to a specific version or the previous active deployment.`,
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		version := ""
		if len(args) > 1 {
			version = args[1]
		}

		c := newClient()
		resp, err := c.Rollback(appName, version)
		if err != nil {
			return fmt.Errorf("rollback: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Rolled back %s: %s\n", appName, resp.Message)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(rollbackCmd)
}
