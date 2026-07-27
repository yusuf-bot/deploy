package cmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"deploy/internal/config"
	"deploy/internal/state"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var noSystemd bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the deploy environment",
	Long: `Creates ~/.deploy directory, SQLite database, and default config.
If run as root, also sets up a systemd service (use --no-systemd to skip).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.InitDir(); err != nil {
			return fmt.Errorf("init directory: %w", err)
		}
		fmt.Printf("Created %s\n", config.DeployDirPath())

		if err := config.InitSocketDir(); err != nil {
			return fmt.Errorf("init socket dir: %w", err)
		}

		db, err := state.OpenDB(config.DBPath())
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		if err := state.Migrate(db); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}
		fmt.Printf("Created database %s\n", config.DBPath())

		// Generate master key for secret encryption
		if _, err := state.EnsureMasterKey(config.DeployDirPath()); err != nil {
			return fmt.Errorf("generate master key: %w", err)
		}
		fmt.Printf("Created master key %s\n", config.DeployDirPath()+"/"+state.MasterKeyFile)

		configPath := config.ConfigPath()
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			cfg := config.DefaultConfig()
			data, err := yaml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}
			if err := os.WriteFile(configPath, data, 0600); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Printf("Created config %s\n", configPath)
		} else {
			fmt.Printf("Config %s already exists, skipping\n", configPath)
		}

		// Caddy setup
		caddyDir := config.CaddyDir()
		if err := os.MkdirAll(filepath.Join(caddyDir, "sites"), 0755); err != nil {
			return fmt.Errorf("create caddy dir: %w", err)
		}
		fmt.Printf("Created %s\n", caddyDir)

		caddyPath, caddyErr := exec.LookPath("caddy")
		if caddyErr == nil {
			fmt.Printf("  caddy:      %s\n", caddyPath)
		} else {
			fmt.Println("  caddy:      not found (domain management disabled)")
			if isInteractive() {
				fmt.Println()
				fmt.Print("Download Caddy v2.8.4? (Recommended) [Y/n]: ")
				var response string
				fmt.Scanln(&response)
				if response == "" || strings.EqualFold(response, "y") || strings.EqualFold(response, "yes") {
					if err := downloadCaddy(caddyDir); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to download caddy: %v\n", err)
						fmt.Fprintf(os.Stderr, "Install manually: https://caddyserver.com/download\n")
					}
				}
			}
		}

		if os.Geteuid() == 0 && !noSystemd {
			if _, err := os.Stat("/run/systemd/system"); err == nil {
				if err := setupSystemd(); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: systemd setup failed: %v\n", err)
				} else {
					fmt.Println("Systemd service installed and enabled")
				}
			} else {
				fmt.Println("Systemd not detected, skipping service setup")
			}
		}

		fmt.Println("Deploy initialized successfully!")
		return nil
	},
}

// isInteractive returns true if stdout is a terminal (char device).
func isInteractive() bool {
	fi, _ := os.Stdout.Stat()
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// downloadCaddy downloads and extracts the Caddy binary for the current platform.
func downloadCaddy(dir string) error {
	version := "v2.8.4"
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	url := fmt.Sprintf("https://github.com/caddyserver/caddy/releases/download/%s/caddy_%s_%s_%s.tar.gz",
		version, strings.TrimPrefix(version, "v"), goos, goarch)

	fmt.Printf("Downloading Caddy %s for %s/%s...\n", version, goos, goarch)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("decompress: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	destPath := filepath.Join(dir, "caddy")

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if header.Name == "caddy" {
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY, 0755)
			if err != nil {
				return fmt.Errorf("create file: %w", err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("write file: %w", err)
			}
			out.Close()

			fmt.Printf("Caddy %s downloaded to %s\n", version, destPath)
			fmt.Println("Add to PATH or run: sudo cp", destPath, "/usr/local/bin/caddy")
			return nil
		}
	}

	return fmt.Errorf("caddy binary not found in archive")
}

func setupSystemd() error {
	unit := fmt.Sprintf(`[Unit]
Description=Deploy Daemon
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s daemon
Restart=always
RestartSec=5
StartLimitIntervalSec=0

[Install]
WantedBy=multi-user.target
`, os.Args[0])

	unitPath := "/etc/systemd/system/deploy.service"
	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := runCommand("systemctl", "enable", "deploy.service"); err != nil {
		return fmt.Errorf("enable service: %w", err)
	}
	return nil
}

func init() {
	// initCmd is added to rootCmd in root.go's init()
}
