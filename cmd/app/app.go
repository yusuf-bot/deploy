package app

import (
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "app",
	Short: "Manage applications",
	Long:  `Create, list, start, stop, and manage applications.`,
}

func init() {
	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(lsCmd)
	Cmd.AddCommand(infoCmd)
}
