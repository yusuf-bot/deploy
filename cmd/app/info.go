package app

import (
	"encoding/json"
	"fmt"

	"deploy/internal/client"
	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info <name>",
	Short: "Show application details",
	Long:  `Display detailed information about an application, including container state.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		c := client.New(config.SocketPath())
		app, err := c.GetApp(name)
		if err != nil {
			return fmt.Errorf("get app: %w", err)
		}

		if cmd.Flags().Changed("json") {
			data, _ := json.MarshalIndent(app, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Name:        %s\n", app.Name)
		fmt.Printf("ID:          %s\n", app.ID)
		fmt.Printf("Status:      %s\n", app.Status)
		fmt.Printf("Port:        %d\n", app.Port)
		fmt.Printf("Image:       %s\n", app.Image)
		fmt.Printf("Container:   %s\n", app.ContainerID)
		if len(app.Env) > 0 {
			fmt.Printf("Environment:\n")
			for k, v := range app.Env {
				fmt.Printf("  %s=%s\n", k, v)
			}
		}
		return nil
	},
}

func init() {
}
