package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"deploy/internal/types"

	"gopkg.in/yaml.v3"
)

// defaultDeployConfig returns a DeployConfig with sensible defaults.
func defaultDeployConfig() *types.DeployConfig {
	return &types.DeployConfig{
		Build: types.BuildConfig{
			Context:    ".",
			Dockerfile: "Dockerfile",
		},
		Health: types.HealthConfig{
			Path:         "/health",
			InitialDelay: "3s",
			Interval:     "5s",
			Timeout:      "3s",
			Retries:      3,
		},
	}
}

// appNamePattern matches ^[a-z][a-z0-9-]*$.
var appNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// LoadDeployConfig reads and validates a deploy.yml file.
func LoadDeployConfig(path string) (*types.DeployConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open deploy config: %w", err)
	}
	defer f.Close()

	cfg := defaultDeployConfig()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse deploy config: %w", err)
	}

	if err := validateDeployConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid deploy config: %w", err)
	}

	return cfg, nil
}

// validateDeployConfig checks all fields of a parsed DeployConfig.
func validateDeployConfig(cfg *types.DeployConfig) error {
	if cfg.App == "" {
		return fmt.Errorf("app name is required")
	}
	if !appNamePattern.MatchString(cfg.App) {
		return fmt.Errorf("app name %q must match %s", cfg.App, "^[a-z][a-z0-9-]*$")
	}

	// Build defaults
	if cfg.Build.Context == "" {
		cfg.Build.Context = "."
	}
	if cfg.Build.Dockerfile == "" {
		cfg.Build.Dockerfile = "Dockerfile"
	}

	// Validate ports
	for i, p := range cfg.Ports {
		if p.Container < 1 || p.Container > 65535 {
			return fmt.Errorf("ports[%d].container: must be 1-65535, got %d", i, p.Container)
		}
		if p.Host > 0 && (p.Host < 1 || p.Host > 65535) {
			return fmt.Errorf("ports[%d].host: must be 1-65535, got %d", i, p.Host)
		}
	}

	// Validate durations in health config
	if cfg.Health.InitialDelay != "" {
		if _, err := time.ParseDuration(cfg.Health.InitialDelay); err != nil {
			return fmt.Errorf("health.initial-delay: invalid duration %q: %w", cfg.Health.InitialDelay, err)
		}
	}
	if cfg.Health.Interval != "" {
		if _, err := time.ParseDuration(cfg.Health.Interval); err != nil {
			return fmt.Errorf("health.interval: invalid duration %q: %w", cfg.Health.Interval, err)
		}
	}
	if cfg.Health.Timeout != "" {
		if _, err := time.ParseDuration(cfg.Health.Timeout); err != nil {
			return fmt.Errorf("health.timeout: invalid duration %q: %w", cfg.Health.Timeout, err)
		}
	}
	if cfg.Health.Retries < 0 {
		return fmt.Errorf("health.retries: must be non-negative, got %d", cfg.Health.Retries)
	}

	return nil
}

// DetectStack attempts to detect the application stack from the project directory.
// Returns one of: "dockerfile", "docker-compose", "static", "node", "go", or "unknown".
func DetectStack(path string) string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "unknown"
	}

	hasDockerfile := false
	hasCompose := false
	hasPackageJSON := false
	hasGoMod := false
	hasIndexHTML := false

	for _, e := range entries {
		name := strings.ToLower(e.Name())
		switch name {
		case "dockerfile":
			hasDockerfile = true
		case "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml":
			hasCompose = true
		case "package.json":
			hasPackageJSON = true
		case "go.mod":
			hasGoMod = true
		case "index.html":
			hasIndexHTML = true
		}
	}

	switch {
	case hasCompose:
		return "docker-compose"
	case hasDockerfile:
		return "dockerfile"
	case hasGoMod:
		return "go"
	case hasPackageJSON:
		return "node"
	case hasIndexHTML || hasStaticFile(path):
		return "static"
	default:
		return "unknown"
	}
}

// hasStaticFile checks for common static-site markers.
func hasStaticFile(path string) bool {
	extensions := []string{".html", ".htm", ".css", ".js"}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(strings.ToLower(e.Name()))
		for _, want := range extensions {
			if ext == want {
				return true
			}
		}
	}
	return false
}
