// Package app provides the 'deploy app' subcommands.
package app

import (
	"github.com/spf13/cobra"
)

// Cmd is the parent command for all app subcommands.
var Cmd = &cobra.Command{
	Use:   "app",
	Short: "Manage applications",
	Hidden: true,
	Long:  `Create, list, start, stop, and manage applications.`,
}

func init() {
	Cmd.AddCommand(createCmd)
	Cmd.AddCommand(lsCmd)
	Cmd.AddCommand(infoCmd)
	Cmd.AddCommand(rmCmd)
	Cmd.AddCommand(startCmd)
	Cmd.AddCommand(stopCmd)
	Cmd.AddCommand(logsCmd)
}
