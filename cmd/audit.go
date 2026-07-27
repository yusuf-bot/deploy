package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"deploy/internal/audit"

	"github.com/spf13/cobra"
)

var auditTail int

var auditCmd = &cobra.Command{
	Use:   "audit [app-name]",
	Short: "Show recent deploy audit entries",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := audit.ReadRecent(auditTail)
		if err != nil {
			return fmt.Errorf("audit: %w", err)
		}

		// Filter by app name if provided
		if len(args) > 0 {
			appName := args[0]
			var filtered []audit.Entry
			for _, e := range entries {
				if e.App == appName {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(entries, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(entries) == 0 {
			fmt.Println("No audit entries found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "TIME\tACTION\tAPP\tVERSION\tDURATION\tRESULT")
		fmt.Fprintln(w, "----\t------\t---\t-------\t--------\t------")
		for _, e := range entries {
			version := e.Version
			if version == "" {
				version = "-"
			}
			dur := fmt.Sprintf("%dms", e.DurationMs)
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				e.Time.Format(time.RFC3339), e.Action, e.App, version, dur, e.Result)
		}
		w.Flush()
		return nil
	},
}

func init() {
	auditCmd.Flags().IntVarP(&auditTail, "tail", "n", 20, "Number of recent entries")
	rootCmd.AddCommand(auditCmd)
}
