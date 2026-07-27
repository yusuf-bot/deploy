package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var rmForce bool

var rmCmd = &cobra.Command{
	Use:   "rm <app-name>",
	Short: "Remove an app (clean teardown)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if !rmForce {
			fmt.Printf("Remove app %q? This will stop the container, delete domains, secrets, and images. [y/N]: ", name)
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))
			if input != "y" && input != "yes" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		c := newClient()
		return c.RemoveApp(name)
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "Skip confirmation")
	rootCmd.AddCommand(rmCmd)
}
