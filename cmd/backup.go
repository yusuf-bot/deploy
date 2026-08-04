package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var backupCmd = &cobra.Command{
	Use:   "backup [app]",
	Short: "Create a full system or per-app backup",
	Long: `Creates a complete backup of the deploy system including the SQLite
database (via VACUUM INTO), master key, audit log, images, and Caddy config.
The backup is saved as a timestamped tar.gz archive.

With an optional app name, creates a per-app backup archive containing that
app's image tarball(s) plus a JSON export of its DB rows (app, deployments,
secrets, domains, port allocation). Secrets stay encrypted — never plaintext.
Restore a per-app backup with 'deploy restore <file> <app>' while the daemon
is running.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		if len(args) == 1 {
			path, err := c.CreateAppBackup(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Backup created: %s\n", path)
			return nil
		}
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
