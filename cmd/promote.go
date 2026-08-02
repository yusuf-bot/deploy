package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"deploy/internal/types"

	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var promoteDir string

var promoteCmd = &cobra.Command{
	Use:   "promote [app-name]",
	Short: "Deploy a new version",
	Long:  `Build and deploy a new version. App name can come from deploy.yml or be provided as argument.`,
	Args:  cobra.MaximumNArgs(1),
	Hidden: true,
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


		absDir, err := filepath.Abs(promoteDir)
		if err != nil {
			return fmt.Errorf("resolve directory: %w", err)
		}

		c := newClient()
		var resp *types.PromoteResponse

		if waitFlag {
			resp, err = c.PromoteStream(appName, absDir, func(evt types.ProgressEvent) {
				icon := " ▶"
				switch evt.Status {
				case "done":
					icon = " ✓"
				case "error":
					icon = " ✗"
				}
				if evt.Message != "" {
					fmt.Fprintf(os.Stderr, "%s %s\n", icon, evt.Message)
				}
			})
		} else {
			resp, err = c.Promote(appName, absDir, false)
		}

		if err != nil {
			return fmt.Errorf("deploy: %w", err)
		}

		if jsonFlag {
			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Deployed %s: %s\n", appName, resp.Message)
		return nil
	},
}

func init() {
	promoteCmd.Flags().StringVar(&promoteDir, "dir", ".", "App directory with deploy.yml")
	rootCmd.AddCommand(promoteCmd)
}