package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Create a full system backup",
	Long: `Creates a complete backup of the deploy system including the SQLite
database (via VACUUM INTO), master key, audit log, images, and Caddy config.
The backup is saved as a timestamped tar.gz archive.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		path, err := c.CreateBackup()
		if err != nil {
			return err
		}
		fmt.Printf("Backup created: %s\n", path)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(backupCmd)
}
