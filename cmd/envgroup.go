package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var envGroupCmd = &cobra.Command{
	Use:   "env-group",
	Short: "Manage environment groups",
	Long:  `Create, configure, and assign environment groups to applications.`,
}

var envGroupCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an environment group",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		client, _ := cmd.Flags().GetString("client")

		c := newClient()
		group, err := c.CreateEnvGroup(name, client)
		if err != nil {
			return fmt.Errorf("create env group: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(group, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Environment group %q created (id=%d)\n", group.Name, group.ID)
		return nil
	},
}

var envGroupSetCmd = &cobra.Command{
	Use:   "set <group-name> <key>=<value>",
	Short: "Set an environment variable in a group",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		groupName := args[0]
		kv := args[1]

		idx := strings.Index(kv, "=")
		if idx <= 0 {
			return fmt.Errorf("invalid format %q: expected KEY=VALUE", kv)
		}
		key := kv[:idx]
		value := kv[idx+1:]

		c := newClient()
		if err := c.SetEnvGroupVar(groupName, key, value); err != nil {
			return fmt.Errorf("set env group var: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]string{"message": fmt.Sprintf("variable %q set in group %q", key, groupName)}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Variable %q set in group %q\n", key, groupName)
		return nil
	},
}

var envGroupAddCmd = &cobra.Command{
	Use:   "add <group-name> <app-name>",
	Short: "Add an app to an environment group",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		groupName := args[0]
		appName := args[1]

		c := newClient()
		if err := c.SetAppEnvGroup(appName, groupName); err != nil {
			return fmt.Errorf("add app to env group: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]string{"message": fmt.Sprintf("app %q added to group %q", appName, groupName)}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("App %q added to group %q\n", appName, groupName)
		return nil
	},
}

var envGroupRmCmd = &cobra.Command{
	Use:   "rm <app-name>",
	Short: "Remove an app from its environment group",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]

		c := newClient()
		if err := c.ClearAppEnvGroup(appName); err != nil {
			return fmt.Errorf("remove app from env group: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]string{"message": fmt.Sprintf("app %q removed from its group", appName)}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("App %q removed from its group\n", appName)
		return nil
	},
}

var envGroupLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all environment groups",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		groups, err := c.ListEnvGroups()
		if err != nil {
			return fmt.Errorf("list env groups: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(groups, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(groups) == 0 {
			fmt.Println("No environment groups")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tCLIENT\tVARS")
		fmt.Fprintln(w, "----\t------\t----")
		for _, g := range groups {
			fmt.Fprintf(w, "%s\t%s\t%d\n", g.Name, g.Client, g.VarCount)
		}
		w.Flush()
		return nil
	},
}

func init() {
	envGroupCreateCmd.Flags().String("client", "", "Client name")
	envGroupCmd.AddCommand(envGroupCreateCmd)
	envGroupCmd.AddCommand(envGroupSetCmd)
	envGroupCmd.AddCommand(envGroupAddCmd)
	envGroupCmd.AddCommand(envGroupRmCmd)
	envGroupCmd.AddCommand(envGroupLsCmd)
	rootCmd.AddCommand(envGroupCmd)
}
