package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var gitCmd = &cobra.Command{
	Use:   "git",
	Short: "Manage git-based deployments",
}

var gitSetupCmd = &cobra.Command{
	Use:   "setup <app-name>",
	Short: "Set up git push deploy for an app",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Validate app name contains only safe characters
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
			return fmt.Errorf("invalid app name: %q (must match [a-zA-Z0-9_-]+)", name)
		}

		// 1. Verify app exists
		c := newClient()
		_, err := c.GetApp(name)
		if err != nil {
			return fmt.Errorf("app %q not found: %w", name, err)
		}

		// 2. Create bare repo directory
		deployDir := config.DeployDirPath()
		bareDir := filepath.Join(deployDir, "git", name+".git")
		os.MkdirAll(filepath.Dir(bareDir), 0700)

		// 3. Init bare repo
		initCmd := exec.Command("git", "init", "--bare", bareDir)
		initCmd.Stdout = os.Stdout
		initCmd.Stderr = os.Stderr
		if err := initCmd.Run(); err != nil {
			return fmt.Errorf("git init: %w", err)
		}

		// 4. Find deploy binary path
		deployBin, err := exec.LookPath("deploy")
		if err != nil {
			deployBin = "/usr/local/bin/deploy"
		}

		// 5. Write post-receive hook
		checkoutDir := filepath.Join(deployDir, "git", "checkouts", name)
		hooksDir := filepath.Join(bareDir, "hooks")
		os.MkdirAll(hooksDir, 0755)

		hookContent := fmt.Sprintf(`#!/bin/sh
# Deploy post-receive hook for %[1]s
DEPLOY_BIN='%[2]s'
CHECKOUT_DIR='%[3]s'

while read oldrev newrev refname; do
    branch=$(basename "$refname")
    case "$branch" in
        main|master)
            mkdir -p "$CHECKOUT_DIR"
            git --work-tree="$CHECKOUT_DIR" checkout -f "$branch"
            cd "$CHECKOUT_DIR" && exec $DEPLOY_BIN up %[1]s --dir "$CHECKOUT_DIR"
            ;;
    esac
done
`, name, deployBin, checkoutDir)

		hookPath := filepath.Join(hooksDir, "post-receive")
		if err := os.WriteFile(hookPath, []byte(hookContent), 0755); err != nil {
			return fmt.Errorf("write hook: %w", err)
		}

		// 6. Print instructions
		fmt.Printf("Git deploy set up for %q.\n", name)
		fmt.Printf("  Bare repo: %s\n", bareDir)
		fmt.Println()
		fmt.Printf("Add remote:\n")
		fmt.Printf("  git remote add deploy ssh://<user>@<host>%s\n", bareDir)
		fmt.Println()
		fmt.Printf("Deploy:\n")
		fmt.Printf("  git push deploy main\n")

		return nil
	},
}

func init() {
	gitCmd.AddCommand(gitSetupCmd)
	rootCmd.AddCommand(gitCmd)
}
