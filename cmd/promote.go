package cmd

import (
	"encoding/json"
	"fmt"

	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var promoteDir string

var promoteCmd = &cobra.Command{
	Use:   "promote [app-name]",
	Short: "Promote a new deployment",
	Long:  `Build and deploy a new version. App name can come from deploy.yml or be provided as argument.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := ""
		if len(args) > 0 {
			appName = args[0]
		}

		// If app name not provided, try reading from deploy.yml
		if appName == "" {
			cfg, err := config.LoadDeployConfig(promoteDir + "/deploy.yml")
			if err != nil {
				return fmt.Errorf("app name not provided and cannot load deploy.yml: %w", err)
			}
			appName = cfg.App
			if appName == "" {
				return fmt.Errorf("app name not provided and deploy.yml has no 'app' field")
			}
		}

		wait := waitFlag

		c := newClient()
		resp, err := c.Promote(appName, promoteDir, wait)
		if err != nil {
			return fmt.Errorf("promote: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Promoted %s: %s\n", appName, resp.Message)
		return nil
	},
}

func init() {
	promoteCmd.Flags().StringVar(&promoteDir, "dir", ".", "App directory with deploy.yml")
	rootCmd.AddCommand(promoteCmd)
}
