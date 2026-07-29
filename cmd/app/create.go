package app

import (
	"encoding/json"
	"fmt"

	"deploy/internal/client"
	"deploy/internal/config"
	"deploy/internal/types"

	"github.com/spf13/cobra"
)

var (
	image string
	appPort  int
	env   []string
)

var createCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new application",
	Long:  `Register a new application. Requires --image and --port.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if err := config.ValidateName(name); err != nil {
			return fmt.Errorf("invalid name: %w", err)
		}
		if image == "" {
			return fmt.Errorf("--image is required")
		}
		if err := config.ValidatePort(appPort); err != nil {
			return fmt.Errorf("invalid port: %w", err)
		}

		envMap := make(map[string]string)
		for _, e := range env {
			k, v, err := config.ParseEnvVar(e)
			if err != nil {
				return err
			}
			envMap[k] = v
		}

		c := client.New(config.SocketPath())
		app, err := c.CreateApp(types.CreateAppRequest{
			Name:  name,
			Image: image,
			Port:  appPort,
			Env:   envMap,
		})
		if err != nil {
			return fmt.Errorf("create app: %w", err)
		}

		if isJSONFlag(cmd) {
			data, err := json.MarshalIndent(app, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
		} else {
			fmt.Printf("App %q created (id: %s)\n", app.Name, app.ID[:8])
		}
		return nil
	},
}

func init() {
	createCmd.Flags().StringVar(&image, "image", "", "Docker image (required)")
	createCmd.Flags().IntVar(&appPort, "port", 0, "Host port (required, 1-65535)")
	createCmd.Flags().StringArrayVar(&env, "env", nil, "Environment variable KEY=VALUE (repeatable)")
	createCmd.MarkFlagRequired("image")
	createCmd.MarkFlagRequired("port")
}

func isJSONFlag(cmd *cobra.Command) bool {
	if f := cmd.Flags().Lookup("json"); f != nil && f.Value.String() == "true" {
		return true
	}
	if f := cmd.Root().Flags().Lookup("json"); f != nil && f.Value.String() == "true" {
		return true
	}
	if f := cmd.Parent().Flags().Lookup("json"); f != nil && f.Value.String() == "true" {
		return true
	}
	return false
}

func isAsyncFlag(cmd *cobra.Command) bool {
	if f := cmd.Flags().Lookup("async"); f != nil && f.Value.String() == "true" {
		return true
	}
	if f := cmd.Root().Flags().Lookup("async"); f != nil && f.Value.String() == "true" {
		return true
	}
	return false
}