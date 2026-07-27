package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitDescribe(t *testing.T) {
	// Create a temp git repo
	tmpDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init failed (git may not be available): %s, %v", out, err)
	}

	// Configure git user for commit
	exec.Command("git", "-C", tmpDir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", tmpDir, "config", "user.name", "Test").Run()

	// Create a commit
	readme := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(readme, []byte("test"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	exec.Command("git", "-C", tmpDir, "add", ".").Run()
	exec.Command("git", "-C", tmpDir, "commit", "-m", "initial").Run()

	// Tag it
	exec.Command("git", "-C", tmpDir, "tag", "v1.0.0").Run()

	// Test GitDescribe
	tag, err := GitDescribe(tmpDir)
	if err != nil {
		t.Fatalf("GitDescribe: %v", err)
	}
	if !strings.HasPrefix(tag, "v1.0.0") {
		t.Errorf("expected tag starting with v1.0.0, got %s", tag)
	}

	// Test VersionTag
	vt := VersionTag(tmpDir)
	if !strings.HasPrefix(vt, "v1.0.0") {
		t.Errorf("expected VersionTag starting with v1.0.0, got %s", vt)
	}
}

func TestVersionTagFallback(t *testing.T) {
	// Use a non-git directory to force fallback
	tmpDir := t.TempDir()

	vt := VersionTag(tmpDir)
	if !strings.HasPrefix(vt, "ts-") {
		t.Errorf("expected fallback starting with ts-, got %s", vt)
	}
}

func TestGitDescribeNotARepo(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := GitDescribe(tmpDir)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
}
