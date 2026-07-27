package cmd

import (
	"fmt"


	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Manage development containers",
}

var devStartCmd = &cobra.Command{
	Use:   "start <app-name>",
	Short: "Start a dev container with volume mounts",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		resp, err := c.DevStart(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Dev container for %q started (container: %s)\n", args[0], resp.Container)
		return nil
	},
}

var devStopCmd = &cobra.Command{
	Use:   "stop <app-name>",
	Short: "Stop and remove a dev container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()
		_, err := c.DevStop(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Dev container for %q stopped\n", args[0])
		return nil
	},
}

func init() {
	devCmd.AddCommand(devStartCmd, devStopCmd)
	rootCmd.AddCommand(devCmd)
}
