package cmd

import (
	"archive/tar"
	"database/sql"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

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
		// If running as root via sudo, switch to the real user's home
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && os.Geteuid() == 0 {
			usr, err := user.Lookup(sudoUser)
			if err == nil {
				os.Setenv("DEPLOY_HOME", filepath.Join(usr.HomeDir, config.DeployDir))
			}
		}
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
		masterKey, err := state.EnsureMasterKey(config.DeployDirPath())
		if err != nil {
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
					fmt.Println("Systemd service installed, enabled, and started")
				}
			} else {
				fmt.Println("Systemd not detected, skipping service setup")
			}
		}

		chownToUser(config.DeployDirPath(), os.Getenv("SUDO_USER"))
		fmt.Println("Deploy initialized successfully!")
		if isInteractive() {
			runInteractiveWizard(db, masterKey)
		}
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

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
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
			// Atomic write: download to temp file then rename
			tmpPath := destPath + ".tmp"
			out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return fmt.Errorf("create temp file: %w", err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				os.Remove(tmpPath)
				return fmt.Errorf("write file: %w", err)
			}
			out.Close()
			if err := os.Rename(tmpPath, destPath); err != nil {
				os.Remove(tmpPath)
				return fmt.Errorf("rename temp file: %w", err)
			}
			os.Chmod(destPath, 0755)

			fmt.Printf("Caddy %s downloaded to %s\n", version, destPath)
			fmt.Println("Add to PATH or run: sudo cp", destPath, "/usr/local/bin/caddy")
			return nil
		}
	}

	return fmt.Errorf("caddy binary not found in archive")
}

func chownToUser(dir string, sudoUser string) {
	if sudoUser == "" || os.Geteuid() != 0 {
		return
	}
	usr, err := user.Lookup(sudoUser)
	if err != nil {
		log.Printf("warning: cannot look up user %s: %v", sudoUser, err)
		return
	}
	uid, _ := strconv.Atoi(usr.Uid)
	gid, _ := strconv.Atoi(usr.Gid)
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := os.Chown(path, uid, gid); err != nil {
			log.Printf("warning: chown %s: %v", path, err)
		}
		return nil
	})
}

func setupSystemd() error {
	// Detect the user who ran sudo (if any)
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" {
		sudoUser = "root"
	}

	// Resolve user's home directory for DEPLOY_HOME
	userHome := "/root"
	if sudoUser != "" && sudoUser != "root" {
		if usr, err := user.Lookup(sudoUser); err == nil {
			userHome = usr.HomeDir
		}
	}

	unit := fmt.Sprintf(`[Unit]
Description=Deploy Daemon
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
ExecStart=%s daemon
ExecStartPre=/bin/mkdir -p /var/run/deploy
ExecStartPre=/bin/chmod 0770 /var/run/deploy
ExecStartPost=/bin/chmod 0770 /var/run/deploy/deploy.sock
Restart=always
RestartSec=5
StartLimitIntervalSec=0
Environment=DEPLOY_DATA_DIR=%s/.deploy

[Install]
WantedBy=multi-user.target
`, os.Args[0], userHome)

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
	if err := runCommand("systemctl", "start", "deploy.service"); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

func runInteractiveWizard(db *sql.DB, masterKey []byte) {
	fmt.Println()
	fmt.Println("--- Interactive Setup ---")
	fmt.Println()

	// Detect Docker
	dockerVer := "not detected"
	if out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output(); err == nil {
		dockerVer = strings.TrimSpace(string(out))
	}
	fmt.Printf("Docker: %s\n", dockerVer)

	// Detect Caddy
	if caddyPath, err := exec.LookPath("caddy"); err == nil {
		fmt.Printf("Caddy:  %s\n", caddyPath)
	} else {
		fmt.Println("Caddy:  not found")
	}

	// Auto-start
	fmt.Println()
	fmt.Print("Enable auto-start on daemon boot? [y/N]: ")
	var autoResp string
	fmt.Scanln(&autoResp)
	if strings.EqualFold(autoResp, "y") || strings.EqualFold(autoResp, "yes") {
		state.SetSetting(db, "auto_start", "true")
	}

	// Summary and next steps
	fmt.Println()
	fmt.Println("--- Setup Complete ---")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Create an app:   deploy up myapp")
	fmt.Println("  2. Deploy:          deploy up myapp")
	fmt.Println("  3. Add domains:     deploy domain add myapp example.com")
	fmt.Println()
}


func init() {
	initCmd.Flags().BoolVar(&noSystemd, "no-systemd", false, "Skip systemd service setup")
	rootCmd.AddCommand(initCmd)
}