package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	auditApp    string
	auditAction string
	auditBy     string
	auditSince  string
	auditUntil  string
	auditLimit  int
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Search and filter audit log entries",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		entries, err := c.Audit(auditApp, auditAction, auditBy, auditSince, auditUntil, auditLimit)
		if err != nil {
			return fmt.Errorf("audit: %w", err)
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
		fmt.Fprintln(w, "TIME\tACTION\tAPP\tBY\tRESULT\tDURATION_MS")
		fmt.Fprintln(w, "----\t------\t---\t--\t------\t-----------")
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n",
				e.Time.Format("2006-01-02 15:04:05"),
				orDash(e.Action), orDash(e.App), orDash(e.InitiatedBy),
				orDash(e.Result), e.DurationMs)
		}
		w.Flush()
		return nil
	},
}

// orDash returns "-" for empty strings so table cells stay aligned.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func init() {
	auditCmd.Flags().StringVar(&auditApp, "app", "", "Filter by app name")
	auditCmd.Flags().StringVar(&auditAction, "action", "", "Filter by action")
	auditCmd.Flags().StringVar(&auditBy, "by", "", "Filter by initiating user")
	auditCmd.Flags().StringVar(&auditSince, "since", "", "Only entries at/after this RFC3339 time")
	auditCmd.Flags().StringVar(&auditUntil, "until", "", "Only entries at/before this RFC3339 time")
	auditCmd.Flags().IntVar(&auditLimit, "limit", 50, "Max entries to return")
	rootCmd.AddCommand(auditCmd)
}
