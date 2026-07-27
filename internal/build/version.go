// Package build provides image building, versioning, and tarball management.
package build

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GitDescribe runs `git describe --tags --always --dirty` in the given repo path.
// Returns the output trimmed of whitespace, or an error if git fails.
func GitDescribe(repoPath string) (string, error) {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git describe: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// VersionTag returns a version string for the given repo path.
// It first attempts `git describe --tags --always --dirty`.
// If that fails (not a git repo, no tags, etc.), it falls back to
// a timestamp-based tag in the form "ts-<unix-nano>".
func VersionTag(repoPath string) string {
	tag, err := GitDescribe(repoPath)
	if err == nil && tag != "" {
		return tag
	}
	return fmt.Sprintf("ts-%d", time.Now().UnixNano())
}
