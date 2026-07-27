package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage daemon settings",
	Long:  `View and set daemon configuration settings (stored in SQLite).`,
}

var configGetCmd = &cobra.Command{
	Use:   "get [key]",
	Short: "Get a setting or all settings",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()

		if len(args) > 0 {
			settings, err := c.GetConfig()
			if err != nil {
				return fmt.Errorf("get config: %w", err)
			}
			val, ok := settings[args[0]]
			if !ok {
				return fmt.Errorf("setting %q not found", args[0])
			}
			if jsonFlag {
				data, _ := json.MarshalIndent(map[string]string{args[0]: val}, "", "  ")
				fmt.Println(string(data))
				return nil
			}
			fmt.Println(val)
			return nil
		}

		settings, err := c.GetConfig()
		if err != nil {
			return fmt.Errorf("get config: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(settings, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(settings) == 0 {
			fmt.Println("(no settings)")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "KEY\tVALUE")
		fmt.Fprintln(w, "---\t-----")

		keys := make([]string, 0, len(settings))
		for k := range settings {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "%s\t%s\n", k, settings[k])
		}
		w.Flush()
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set key=val",
	Short: "Set a setting",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		kv := strings.SplitN(args[0], "=", 2)
		if len(kv) != 2 || kv[0] == "" {
			return fmt.Errorf("invalid format, use key=val")
		}

		c := newClient()
		if err := c.SetConfig(kv[0], kv[1]); err != nil {
			return fmt.Errorf("set config: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]string{"message": fmt.Sprintf("setting %q updated", kv[0])}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Setting %q updated to %q\n", kv[0], kv[1])
		return nil
	},
}

func init() {
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}
