package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var imagesCmd = &cobra.Command{
	Use:   "images",
	Short: "Manage deployment images",
	Hidden: true,
	Long:  `List and remove deployment tarballs.`,
}

var imagesLsCmd = &cobra.Command{
	Use:   "ls [app-name]",
	Short: "List deployment images",
	Long:  `List tarball images for an app or all apps.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient()

		if len(args) > 0 {
			appName := args[0]
			images, err := c.ListImages(appName)
			if err != nil {
				return fmt.Errorf("list images: %w", err)
			}

			if jsonFlag {
				data, _ := json.MarshalIndent(images, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			if len(images) == 0 {
				fmt.Printf("No images for app %q\n", appName)
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "VERSION")
			fmt.Fprintln(w, "-------")
			for _, v := range images {
				fmt.Fprintln(w, v)
			}
			w.Flush()
		} else {
			// List images for all apps
			gs, err := c.GlobalStatus()
			if err != nil {
				return fmt.Errorf("list apps: %w", err)
			}

			for _, app := range gs.Apps {
				images, err := c.ListImages(app.App.Name)
				if err != nil {
					continue
				}
				if len(images) > 0 {
					fmt.Printf("%s:\n", app.App.Name)
					for _, v := range images {
						fmt.Printf("  %s\n", v)
					}
				}
			}
		}
		return nil
	},
}

var imagesRmCmd = &cobra.Command{
	Use:   "rm <app-name> <version>",
	Short: "Remove a deployment image",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		version := args[1]

		c := newClient()
		if err := c.RemoveImage(appName, version); err != nil {
			return fmt.Errorf("remove image: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(map[string]string{"message": fmt.Sprintf("removed %s:%s", appName, version)}, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Removed image %s:%s\n", appName, version)
		return nil
	},
}

func init() {
	imagesCmd.AddCommand(imagesLsCmd)
	imagesCmd.AddCommand(imagesRmCmd)
	rootCmd.AddCommand(imagesCmd)
}
