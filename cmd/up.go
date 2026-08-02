package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	upCmd.Flags().StringVar(&promoteDir, "dir", ".", "App directory with deploy.yml")
	upCmd.Flags().BoolVar(&waitFlag, "wait", true, "Wait for operation to complete")
	rootCmd.AddCommand(upCmd)
}

var upCmd = &cobra.Command{
	Use:   "up [app-name]",
	Short: "Deploy a new version of an application",
	Long:  `Primary deploy command. Build, deploy, and activate a new version.`,
	RunE:  promoteCmd.RunE,
	Args:  promoteCmd.Args,
}
