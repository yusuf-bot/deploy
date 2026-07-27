package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var domainCmd = &cobra.Command{
	Use:   "domain",
	Short: "Manage custom domains for applications",
	Long:  `Add, remove, and list custom domains attached to applications.`,
}

var domainAddCmd = &cobra.Command{
	Use:   "add <app-name> <domain>",
	Short: "Add a domain to an app",
	Long:  `Attach a custom domain (e.g. example.com) to a running application.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		domain := args[1]

		if !strings.Contains(domain, ".") {
			return fmt.Errorf("invalid domain %q: must contain at least one dot", domain)
		}

		c := newClient()
		if err := c.AddDomain(appName, domain); err != nil {
			return fmt.Errorf("add domain: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]string{
				"message": fmt.Sprintf("domain %q added to app %q", domain, appName),
			}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Domain %q added to app %q\n", domain, appName)
		return nil
	},
}

var domainRmCmd = &cobra.Command{
	Use:   "rm <app-name> <domain>",
	Short: "Remove a domain from an app",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		domain := args[1]

		c := newClient()
		if err := c.RemoveDomain(appName, domain); err != nil {
			return fmt.Errorf("remove domain: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]string{
				"message": fmt.Sprintf("domain %q removed from app %q", domain, appName),
			}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Domain %q removed from app %q\n", domain, appName)
		return nil
	},
}

var domainLsCmd = &cobra.Command{
	Use:   "ls [app-name]",
	Short: "List domains",
	Long:  `List all domains, or filter by app name.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := ""
		if len(args) > 0 {
			appName = args[0]
		}

		c := newClient()
		domains, err := c.ListDomains(appName)
		if err != nil {
			return fmt.Errorf("list domains: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(domains, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(domains) == 0 {
			if appName != "" {
				fmt.Printf("No domains for app %q\n", appName)
			} else {
				fmt.Println("No domains")
			}
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "APP\tDOMAIN\tCREATED")
		fmt.Fprintln(w, "---\t------\t-------")
		for _, d := range domains {
			appLabel := d.AppName
			if appLabel == "" {
				appLabel = d.AppID
			}
			created := d.CreatedAt
			if len(created) > 19 {
				created = created[:19]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", appLabel, d.Domain, created)
		}
		w.Flush()
		return nil
	},
}

func init() {
	domainCmd.AddCommand(domainAddCmd)
	domainCmd.AddCommand(domainRmCmd)
	domainCmd.AddCommand(domainLsCmd)
	rootCmd.AddCommand(domainCmd)
}
