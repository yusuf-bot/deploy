package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var secretsRaw bool

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage application secrets",
	Long:  `Set, get, list, and remove secrets for applications.`,
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <app-name> <key>=<value>",
	Short: "Set a secret",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		kv := args[1]

		idx := strings.Index(kv, "=")
		if idx <= 0 {
			return fmt.Errorf("invalid format %q: expected KEY=VALUE", kv)
		}
		key := kv[:idx]
		value := kv[idx+1:]

		c := newClient()
		if err := c.SetSecret(appName, key, value); err != nil {
			return fmt.Errorf("set secret: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]string{"message": fmt.Sprintf("secret %q set", key)}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Secret %q set for app %q\n", key, appName)
		return nil
	},
}

var secretsGetCmd = &cobra.Command{
	Use:   "get <app-name> <key>",
	Short: "Get a secret",
	Long:  `Get a secret value. By default shows masked value; use --raw to show the actual value.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		key := args[1]

		c := newClient()
		secret, err := c.GetSecret(appName, key)
		if err != nil {
			return fmt.Errorf("get secret: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(secret, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if secretsRaw {
			fmt.Printf("%s\n", secret.Value)
		} else {
			fmt.Printf("**** (use --raw to show value)\n")
		}
		return nil
	},
}

var secretsRmCmd = &cobra.Command{
	Use:   "rm <app-name> <key>",
	Short: "Remove a secret",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		key := args[1]

		c := newClient()
		if err := c.RemoveSecret(appName, key); err != nil {
			return fmt.Errorf("remove secret: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]string{"message": fmt.Sprintf("secret %q removed", key)}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Secret %q removed from app %q\n", key, appName)
		return nil
	},
}

var secretsLsCmd = &cobra.Command{
	Use:   "ls <app-name>",
	Short: "List secrets",
	Long:  `List secret keys for an app (values masked).`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]

		c := newClient()
		secrets, err := c.ListSecrets(appName)
		if err != nil {
			return fmt.Errorf("list secrets: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(secrets, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(secrets) == 0 {
			fmt.Printf("No secrets for app %q\n", appName)
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "KEY\tVALUE")
		fmt.Fprintln(w, "---\t-----")
		for _, s := range secrets {
			fmt.Fprintf(w, "%s\t<masked>\n", s.Key)
		}
		w.Flush()
		return nil
	},
}

func init() {
	secretsGetCmd.Flags().BoolVar(&secretsRaw, "raw", false, "Show secret value in plaintext")
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsGetCmd)
	secretsCmd.AddCommand(secretsRmCmd)
	secretsCmd.AddCommand(secretsLsCmd)
	rootCmd.AddCommand(secretsCmd)
}
