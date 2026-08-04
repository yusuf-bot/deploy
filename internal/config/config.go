// Package config handles path resolution, directory initialization, validation, and config loading.
package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	DefaultSocketDir  = "/var/run/deploy"
	DefaultSocketName = "deploy.sock"
	DefaultDBName     = "deploy.db"
	DefaultConfigName = "deploy.yaml"
	DeployDir         = ".deploy"
	Version           = "0.4.0"
)

// Config holds the deploy daemon configuration.
type Config struct {
	SocketPath string `yaml:"socket_path"`
	DBPath     string `yaml:"db_path"`
	LogLevel   string `yaml:"log_level"`
}

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	return &Config{
		SocketPath: SocketPath(),
		DBPath:     DBPath(),
		LogLevel:   "info",
	}
}

// HomeDir returns the user's home directory.
func HomeDir() string {
	usr, err := user.Current()
	if err != nil {
		// Fallback to HOME env var
		if h := os.Getenv("HOME"); h != "" {
			return h
		}
		return "/root"
	}
	return usr.HomeDir
}

// DeployDir returns the path to the deploy data directory.
// Uses DEPLOY_DATA_DIR env var if set, falling back to the legacy
// DEPLOY_HOME env var, otherwise ~/.deploy/.
func DeployDirPath() string {
	if d := os.Getenv("DEPLOY_DATA_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("DEPLOY_HOME"); d != "" {
		return d
	}
	return filepath.Join(HomeDir(), DeployDir)
}

// SocketPath returns the path to the deploy daemon socket.
func SocketPath() string {
	return filepath.Join(DefaultSocketDir, DefaultSocketName)
}

// DBPath returns the path to the SQLite database.
func DBPath() string {
	return filepath.Join(DeployDirPath(), DefaultDBName)
}

// ConfigPath returns the path to the config file.
func ConfigPath() string {
	return filepath.Join(DeployDirPath(), DefaultConfigName)
}

// InitDir creates ~/.deploy/ with 0700 permissions.
func InitDir() error {
	dir := DeployDirPath()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create deploy dir: %w", err)
	}
	return nil
}

// InitSocketDir creates the parent directory for the socket if needed.
// InitSocketDir creates /var/run/deploy with 0770 perms.
func InitSocketDir() error {
	return os.MkdirAll(DefaultSocketDir, 0770)
}

// ValidateName checks that an app name matches ^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("name too long (max 64 characters)")
	}
	matched, err := regexp.MatchString(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`, name)
	if err != nil {
		return fmt.Errorf("name validation error: %w", err)
	}
	if !matched {
		return fmt.Errorf("name must match pattern: alphanumeric, hyphens allowed, must start and end with alphanumeric")
	}
	return nil
}

// ValidatePort checks that port is in the valid range.
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	return nil
}

// ParseEnvVar parses a KEY=VALUE string.
func ParseEnvVar(s string) (string, string, error) {
	idx := strings.Index(s, "=")
	if idx <= 0 {
		return "", "", fmt.Errorf("invalid env format %q: expected KEY=VALUE", s)
	}
	return s[:idx], s[idx+1:], nil
}

// CaddyDir returns the path to the Caddy configuration directory (~/.deploy/caddy).
func CaddyDir() string {
	return filepath.Join(DeployDirPath(), "caddy")
}
