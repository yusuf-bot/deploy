package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status [app-name]",
	Short: "Show deployment status",
	Long:  `Show deployment status for an app or all apps.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()

		if len(args) > 0 {
			appName := args[0]
			resp, err := c.Status(appName)
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}

			if jsonFlag {
				data, _ := json.MarshalIndent(resp, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			fmt.Printf("App: %s\n", resp.App.Name)
			fmt.Printf("Status: %s\n", resp.App.Status)
			fmt.Printf("Port: %d\n", resp.App.Port)
			if resp.ActiveDeployment != nil {
				fmt.Printf("Active Deployment: %s (version: %s)\n",
					resp.ActiveDeployment.ID[:8], resp.ActiveDeployment.Version)
			} else {
				fmt.Println("Active Deployment: (none)")
			}
			fmt.Printf("Deploy In Progress: %v\n", resp.DeployInProgress)
			if len(resp.RecentDeployments) > 0 {
				fmt.Println("\nRecent Deployments:")
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "ID\tVERSION\tSTATUS\tPORT")
				fmt.Fprintln(w, "--\t-------\t------\t----")
				for _, dep := range resp.RecentDeployments {
					fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", dep.ID[:8], dep.Version, dep.Status, dep.Port)
				}
				w.Flush()
			}
		} else {
			resp, err := c.GlobalStatus()
			if err != nil {
				return fmt.Errorf("global status: %w", err)
			}

			if jsonFlag {
				data, _ := json.MarshalIndent(resp, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "APP\tSTATUS\tPORT\tACTIVE VERSION\tIN PROGRESS")
			fmt.Fprintln(w, "---\t------\t----\t--------------\t-----------")
			for _, app := range resp.Apps {
				version := ""
				if app.ActiveDeployment != nil {
					version = app.ActiveDeployment.Version
				}
				inProgress := "no"
				if app.DeployInProgress {
					inProgress = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", app.App.Name, app.App.Status, app.App.Port, version, inProgress)
			}
			w.Flush()
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
