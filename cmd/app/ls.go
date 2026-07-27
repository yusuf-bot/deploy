package app

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"deploy/internal/client"
	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var statusFilter string

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List applications",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(config.SocketPath())
		apps, err := c.ListApps(statusFilter)
		if err != nil {
			return fmt.Errorf("list apps: %w", err)
		}

		if isJSONFlag(cmd) {
			data, _ := json.MarshalIndent(apps, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tPORT\tIMAGE\tCONTAINER")
		fmt.Fprintln(w, "----\t------\t----\t-----\t---------")
		for _, app := range apps {
			cid := app.ContainerID
			if len(cid) > 12 {
				cid = cid[:12]
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", app.Name, app.Status, app.Port, app.Image, cid)
		}
		w.Flush()
		return nil
	},
}

func init() {
	lsCmd.Flags().StringVar(&statusFilter, "status", "", "Filter by status (created|running|stopped|failed|unknown)")
}
