package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilderBuild(t *testing.T) {
	mock := newMockDocker()
	b := NewBuilder(mock)

	// Create a temporary app directory with deploy.yml and a Dockerfile
	appDir := t.TempDir()
	deployYML := `app: test-app
stack: dockerfile
build:
  context: .
  dockerfile: Dockerfile
`
	if err := os.WriteFile(filepath.Join(appDir, "deploy.yml"), []byte(deployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}

	dockerfile := `FROM alpine:latest
CMD ["echo", "hello"]
`
	if err := os.WriteFile(filepath.Join(appDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	result, err := b.Build(context.Background(), appDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if result.Version == "" {
		t.Error("expected non-empty version")
	}
	if result.ImageRef == "" {
		t.Error("expected non-empty image ref")
	}
	if !strings.Contains(result.ImageRef, "test-app:") {
		t.Errorf("expected image ref to contain test-app:, got %s", result.ImageRef)
	}
	if result.TarballPath == "" {
		t.Error("expected non-empty tarball path")
	}
	if result.ImageDigest != "sha256:abc123def456" {
		t.Errorf("expected sha256:abc123def456, got %s", result.ImageDigest)
	}
}

func TestBuilderBuildError(t *testing.T) {
	mock := newMockDocker()
	mock.addFail("ImageBuild", os.ErrPermission)
	b := NewBuilder(mock)

	appDir := t.TempDir()
	deployYML := `app: fail-app
`
	if err := os.WriteFile(filepath.Join(appDir, "deploy.yml"), []byte(deployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}

	_, err := b.Build(context.Background(), appDir)
	if err == nil {
		t.Fatal("expected error from Build")
	}
}

func TestBuilderBuildMissingDeployYML(t *testing.T) {
	mock := newMockDocker()
	b := NewBuilder(mock)

	emptyDir := t.TempDir()
	_, err := b.Build(context.Background(), emptyDir)
	if err == nil {
		t.Fatal("expected error for missing deploy.yml")
	}
}
