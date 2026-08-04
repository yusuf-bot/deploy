package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var usageCmd = &cobra.Command{
	Use:   "usage [app-name]",
	Short: "Show resource usage for apps (CPU, memory, disk)",
	Long: `Display live resource usage for all apps or a single app:
CPU and memory from Docker stats, per-app image disk usage, and docker
system totals. Without an app name, shows an aggregate table for all apps.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}

		c := newClient()
		resp, err := c.Usage(name)
		if err != nil {
			return fmt.Errorf("usage: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(resp.Apps) == 0 {
			fmt.Println("no apps found")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "APP\tSTATUS\tCPU%\tMEM\tMEM LIMIT\tIMAGE DISK")
		for _, a := range resp.Apps {
			mem := "-"
			if a.Running {
				mem = fmt.Sprintf("%.1f MiB", float64(a.MemBytes)/(1024*1024))
			}
			memLimit := "-"
			if a.MemLimit > 0 {
				memLimit = fmt.Sprintf("%.1f MiB", float64(a.MemLimit)/(1024*1024))
			}
			cpu := "-"
			if a.Running {
				cpu = fmt.Sprintf("%.1f", a.CPUPct)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				a.App, a.Status, cpu, mem, memLimit, humanBytes(a.ImageDiskBytes))
		}
		w.Flush()

		sys := resp.System
		fmt.Println()
		fmt.Printf("docker system: %d images (%s, %s reclaimable) | %d containers (%s) | %s volumes | %s build cache\n",
			sys.ImagesTotalCount, humanBytes(sys.ImagesTotalBytes), humanBytes(sys.ImagesReclaimableBytes),
			sys.ContainersTotalCount, humanBytes(sys.ContainersTotalBytes),
			humanBytes(sys.VolumesTotalBytes), humanBytes(sys.BuildCacheTotalBytes))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(usageCmd)
}
