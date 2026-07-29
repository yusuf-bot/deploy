package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	upCmd.Flags().StringVar(&promoteDir, "dir", ".", "App directory with deploy.yml")
	rootCmd.AddCommand(upCmd)
}

var upCmd = &cobra.Command{
	Use:   "up [app-name]",
	Short: "Deploy a new version of an application",
	Long:  `Build, deploy, and activate a new version. Alias for promote.`,
	RunE:  promoteCmd.RunE,
	Args:  promoteCmd.Args,
}
