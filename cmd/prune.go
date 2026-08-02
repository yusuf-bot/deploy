package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	pruneKeepFlag   int
	pruneDryRunFlag bool
	pruneAppFlag    string
)

var pruneCmd = &cobra.Command{
	Use:   "prune [app-name]",
	Short: "Delete old image tarballs to free disk space",
	Long: `Delete the oldest saved image tarballs for each app, keeping the newest N
(default 3). The tarball of the currently-running version is never deleted.

With no app argument, all apps are pruned. Use --app to target a single app.

Flags:
  --keep N       keep the newest N tarballs per app (default 3)
  --dry-run      show what would be deleted without deleting anything
  --app NAME     prune a single app (same as passing the app name positionally)

Suggested weekly cleanup via systemd (create these files yourself; deploy
will not create them for you):

  /etc/systemd/system/deploy-prune.service:
    [Unit]
    Description=deploy: prune old image tarballs
    After=deploy.service
    [Service]
    Type=oneshot
    ExecStart=/usr/local/bin/deploy prune --keep 3

  /etc/systemd/system/deploy-prune.timer:
    [Unit]
    Description=deploy: weekly image tarball prune
    [Timer]
    OnCalendar=weekly
    Persistent=true
    [Install]
    WantedBy=timers.target

  Then run: sudo systemctl daemon-reload && sudo systemctl enable --now deploy-prune.timer
`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := pruneAppFlag
		if len(args) > 0 {
			if appName != "" && appName != args[0] {
				return fmt.Errorf("app given both as argument (%q) and --app (%q)", args[0], appName)
			}
			appName = args[0]
		}

		c := newClient()
		resp, err := c.Prune(appName, pruneKeepFlag, pruneDryRunFlag)
		if err != nil {
			return fmt.Errorf("prune: %w", err)
		}

		if jsonFlag {
			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		for _, app := range resp.Apps {
			fmt.Fprintf(w, "%s:\n", app.App)
			for _, f := range app.Removed {
				fmt.Fprintf(w, "  \u2716 %s (%s)\n", f.Version, humanBytes(f.SizeBytes))
			}
			for _, f := range app.Protected {
				fmt.Fprintf(w, "  \u2605 %s (%s) — running version, kept\n", f.Version, humanBytes(f.SizeBytes))
			}
			for _, f := range app.Kept {
				fmt.Fprintf(w, "  \u2713 %s (%s)\n", f.Version, humanBytes(f.SizeBytes))
			}
		}
		w.Flush()

		if resp.DryRun {
			fmt.Printf("\nDry run — nothing deleted. Would free %s.\n", humanBytes(resp.TotalFreedBytes))
		} else {
			fmt.Printf("\nFreed %s.\n", humanBytes(resp.TotalFreedBytes))
		}
		return nil
	},
}

// humanBytes formats a byte count as a human-readable string.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func init() {
	pruneCmd.Flags().IntVar(&pruneKeepFlag, "keep", 3, "keep the newest N tarballs per app")
	pruneCmd.Flags().BoolVar(&pruneDryRunFlag, "dry-run", false, "show what would be deleted without deleting")
	pruneCmd.Flags().StringVar(&pruneAppFlag, "app", "", "prune a single app by name")
	rootCmd.AddCommand(pruneCmd)
}
