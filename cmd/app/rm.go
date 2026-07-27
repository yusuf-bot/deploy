package app

import (
	"encoding/json"
	"fmt"

	"deploy/internal/client"
	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var rmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove an application",
	Long:  `Delete an application from the registry. Fails if the app is running.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		c := client.New(config.SocketPath())
		if err := c.DeleteApp(name); err != nil {
			return fmt.Errorf("delete app: %w", err)
		}

		if cmd.Flags().Changed("json") {
			data, _ := json.Marshal(map[string]string{"message": fmt.Sprintf("app %q deleted", name)})
			fmt.Println(string(data))
		} else {
			fmt.Printf("App %q deleted\n", name)
		}
		return nil
	},
}

func init() {
}
