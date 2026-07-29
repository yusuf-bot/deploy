package cmd

import (
	"encoding/json"
	"net"
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

// --- DNS subcommands (Phase 8) ---

var domainDNSCmd = &cobra.Command{
	Use:   "dns",
	Short: "Manage DNS records for app domains",
}

var domainDNSSyncCmd = &cobra.Command{
	Use:   "sync <app>",
	Short: "Ensure A/AAAA records exist for app's domains",
	Long: `Sync DNS records for all domains attached to an app.
Creates A records (--ipv4) and AAAA records (--ipv6) pointing to your server.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		ipv4, _ := cmd.Flags().GetString("ipv4")
		ipv6, _ := cmd.Flags().GetString("ipv6")
		if ipv4 == "" && ipv6 == "" {
			// Auto-detect IPv4
			detected, err := detectPublicIP()
			if err == nil && detected != "" {
				ipv4 = detected
				fmt.Printf("auto-detected public IPv4: %s\n", ipv4)
			}
		}

		if ipv4 == "" && ipv6 == "" {
			return fmt.Errorf("could not detect public IP and no --ipv4 or --ipv6 provided")
		}
		resp, err := c.DNSSync(args[0], ipv4, ipv6)
		if err != nil {
			return err
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		for _, r := range resp.Results {
			status := "created"
			if r.Existed {
				status = "exists"
			}
			fmt.Printf("  %s %s -> %s (%s)\n", r.Domain, r.Type, r.Value, status)
		}
		for _, e := range resp.Errors {
			fmt.Fprintf(os.Stderr, "  error: %s\n", e)
		}
		return nil
	},
}

var domainDNSListCmd = &cobra.Command{
	Use:   "list <app>",
	Short: "List DNS records for app's domains",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		records, err := c.DNSList(args[0])
		if err != nil {
			return err
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(records, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(records) == 0 {
			fmt.Printf("No DNS records for app %q\n", args[0])
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tTYPE\tVALUE\tTTL")
		fmt.Fprintln(w, "--\t----\t----\t-----\t---")
		for _, r := range records {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", r.ID, r.Name, r.Type, r.Value, r.TTL)
		}
		w.Flush()
		return nil
	},
}

func detectPublicIP() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}
			ipv4 := ipnet.IP.To4()
			if ipv4 != nil {
				return ipv4.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no public IPv4 address found")
}


func init() {
	domainCmd.AddCommand(domainAddCmd)
	domainCmd.AddCommand(domainRmCmd)
	domainCmd.AddCommand(domainLsCmd)
	domainCmd.AddCommand(domainDNSCmd)
	rootCmd.AddCommand(domainCmd)

	domainDNSCmd.AddCommand(domainDNSSyncCmd)
	domainDNSCmd.AddCommand(domainDNSListCmd)

	domainDNSSyncCmd.Flags().String("ipv4", "", "Server IPv4 address for A records")
	domainDNSSyncCmd.Flags().String("ipv6", "", "Server IPv6 address for AAAA records")
}
