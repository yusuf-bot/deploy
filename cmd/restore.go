package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"deploy/internal/config"

	"github.com/spf13/cobra"
)

var restoreCmd = &cobra.Command{
	Use:   "restore <backup-file>",
	Short: "Restore from a backup (daemon must be stopped)",
	Long: `Restores the deploy system from a tar.gz backup archive created by
'deploy backup'. The daemon must be stopped before restoring.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Check daemon isn't running — socket shouldn't exist.
		socketPath := config.SocketPath()
		if _, err := os.Stat(socketPath); err == nil {
			return fmt.Errorf("daemon is running — stop it before restore")
		}

		backupFile := args[0]
		if _, err := os.Stat(backupFile); err != nil {
			return fmt.Errorf("backup file not found: %s", backupFile)
		}

		// Extract to temp dir.
		tmpDir, err := os.MkdirTemp("", "deploy-restore-")
		if err != nil {
			return fmt.Errorf("mktemp: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		tarcmd := exec.Command("tar", "xzf", backupFile, "-C", tmpDir)
		if output, err := tarcmd.CombinedOutput(); err != nil {
			return fmt.Errorf("tar extract: %s: %w", string(output), err)
		}

		// Validate master.key is 32 bytes.
		masterKeyPath := filepath.Join(tmpDir, "master.key")
		masterKeyData, err := os.ReadFile(masterKeyPath)
		if err != nil {
			return fmt.Errorf("read master.key from backup: %w", err)
		}
		if len(masterKeyData) != 32 {
			return fmt.Errorf("invalid master.key in backup: got %d bytes, want 32", len(masterKeyData))
		}

		// Backup existing ~/.deploy/ to ~/.deploy/.pre-restore-<ts>/
		deployDir := config.DeployDirPath()
		preRestoreDir := filepath.Join(deployDir, fmt.Sprintf(".pre-restore-%d", time.Now().Unix()))
		if _, err := os.Stat(deployDir); err == nil {
			if err := os.Rename(deployDir, preRestoreDir); err != nil {
				return fmt.Errorf("backup existing deploy dir: %w", err)
			}
			fmt.Printf("Existing data moved to %s\n", preRestoreDir)
		}

		// Move restored data into place.
		if err := os.Rename(tmpDir, deployDir); err != nil {
			// Cross-device link or other failure — restore original backup.
			os.Rename(preRestoreDir, deployDir)
			return fmt.Errorf("restore: replace failed: %w — original restored", err)
		}
		// Only remove backup if restore succeeded.
		os.RemoveAll(preRestoreDir)

		fmt.Println("Restore complete!")
		fmt.Println("Start daemon with 'deploy daemon'")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(restoreCmd)
}
