package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return path
}

func TestLoadValidDeployConfig(t *testing.T) {
	yaml := `
app: myapp
stack: dockerfile
build:
  context: .
  dockerfile: Dockerfile
  args:
    NODE_VERSION: "20"
ports:
  - container: 3000
    host: 8080
env:
  NODE_ENV: production
health:
  path: /health
  initial-delay: 3s
  interval: 5s
  timeout: 3s
  retries: 3
resources:
  memory: 512m
  cpus: "0.5"
volumes:
  - source: ./data
    target: /app/data
`
	path := writeTempYAML(t, yaml)
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}
	if cfg.App != "myapp" {
		t.Errorf("expected myapp, got %s", cfg.App)
	}
	if cfg.Stack != "dockerfile" {
		t.Errorf("expected dockerfile, got %s", cfg.Stack)
	}
	if cfg.Build.Context != "." {
		t.Errorf("expected ., got %s", cfg.Build.Context)
	}
	if len(cfg.Ports) != 1 || cfg.Ports[0].Container != 3000 || cfg.Ports[0].Host != 8080 {
		t.Errorf("unexpected ports: %+v", cfg.Ports)
	}
	if cfg.Env["NODE_ENV"] != "production" {
		t.Errorf("expected production, got %s", cfg.Env["NODE_ENV"])
	}
	if cfg.Resources.Memory != "512m" {
		t.Errorf("expected 512m, got %s", cfg.Resources.Memory)
	}
	if len(cfg.Volumes) != 1 || cfg.Volumes[0].Source != "./data" {
		t.Errorf("unexpected volumes: %+v", cfg.Volumes)
	}
}

func TestLoadDeployConfigMinimal(t *testing.T) {
	yaml := `app: myapp`
	path := writeTempYAML(t, yaml)
	cfg, err := LoadDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}
	if cfg.App != "myapp" {
		t.Errorf("expected myapp, got %s", cfg.App)
	}
	// Should get defaults
	if cfg.Build.Context != "." {
		t.Errorf("expected default context ., got %s", cfg.Build.Context)
	}
	if cfg.Build.Dockerfile != "Dockerfile" {
		t.Errorf("expected default Dockerfile, got %s", cfg.Build.Dockerfile)
	}
}

func TestLoadDeployConfigMissingApp(t *testing.T) {
	yaml := `env:
  FOO: bar`
	path := writeTempYAML(t, yaml)
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for missing app name")
	}
}

func TestLoadDeployConfigInvalidAppName(t *testing.T) {
	yaml := `app: MyApp-Uppercase`
	path := writeTempYAML(t, yaml)
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for uppercase app name")
	}
}

func TestLoadDeployConfigInvalidAppNameWithUnderscore(t *testing.T) {
	yaml := `app: my_app`
	path := writeTempYAML(t, yaml)
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for underscore in app name")
	}
}

func TestLoadDeployConfigUnknownField(t *testing.T) {
	yaml := `
app: myapp
unknown_field: should_fail
`
	path := writeTempYAML(t, yaml)
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestLoadDeployConfigNestedUnknownField(t *testing.T) {
	yaml := `
app: myapp
build:
  context: .
  unknown_key: oops
`
	path := writeTempYAML(t, yaml)
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for nested unknown field")
	}
}

func TestLoadDeployConfigInvalidPort(t *testing.T) {
	yaml := `
app: myapp
ports:
  - container: 0
    host: 8080
`
	path := writeTempYAML(t, yaml)
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestLoadDeployConfigInvalidHostPort(t *testing.T) {
	yaml := `
app: myapp
ports:
  - container: 3000
    host: 99999
`
	path := writeTempYAML(t, yaml)
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid host port")
	}
}

func TestLoadDeployConfigInvalidDuration(t *testing.T) {
	yaml := `
app: myapp
health:
  interval: not-a-duration
`
	path := writeTempYAML(t, yaml)
	_, err := LoadDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestLoadDeployConfigFileNotFound(t *testing.T) {
	_, err := LoadDeployConfig("/nonexistent/path/deploy.yml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDetectStackDockerfile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx"), 0644)

	stack := DetectStack(dir)
	if stack != "dockerfile" {
		t.Errorf("expected dockerfile, got %s", stack)
	}
}

func TestDetectStackDockerCompose(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:"), 0644)

	stack := DetectStack(dir)
	if stack != "docker-compose" {
		t.Errorf("expected docker-compose, got %s", stack)
	}
}

func TestDetectStackGo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	stack := DetectStack(dir)
	if stack != "go" {
		t.Errorf("expected go, got %s", stack)
	}
}

func TestDetectStackNode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)

	stack := DetectStack(dir)
	if stack != "node" {
		t.Errorf("expected node, got %s", stack)
	}
}

func TestDetectStackStatic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0644)

	stack := DetectStack(dir)
	if stack != "static" {
		t.Errorf("expected static, got %s", stack)
	}
}

func TestDetectStackUnknown(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "random.txt"), []byte("data"), 0644)

	stack := DetectStack(dir)
	if stack != "unknown" {
		t.Errorf("expected unknown, got %s", stack)
	}
}

func TestDetectStackDirNotExist(t *testing.T) {
	stack := DetectStack("/tmp/nonexistent-dir-for-test-abc123")
	if stack != "unknown" {
		t.Errorf("expected unknown for missing dir, got %s", stack)
	}
}
