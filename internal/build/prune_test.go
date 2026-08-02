package build

import (
	"os"
	"path/filepath"
	"testing"
)

// writeLegacyTarball writes a raw .tar file for a version (no compression).
func writeLegacyTarball(t *testing.T, appName, version string, size int) {
	t.Helper()
	dir := appImagesDir(appName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, version+LegacyTarballExt), make([]byte, size), 0600); err != nil {
		t.Fatalf("write tarball %s: %v", version, err)
	}
}

func TestPruneTarballsDryRun(t *testing.T) {
	appName := "prune-dry-app"
	for i := 1; i <= 5; i++ {
		writeLegacyTarball(t, appName, "v"+string(rune('0'+i)), 100+i)
	}

	res, err := PruneTarballs(appName, 2, nil, true)
	if err != nil {
		t.Fatalf("PruneTarballs: %v", err)
	}
	if len(res.Removed) != 3 {
		t.Errorf("dry run: expected 3 removed, got %d (%v)", len(res.Removed), res.Removed)
	}
	if len(res.Kept) != 2 {
		t.Errorf("dry run: expected 2 kept, got %d", len(res.Kept))
	}
	// Dry run must not delete anything
	versions, err := ListTarballs(appName)
	if err != nil {
		t.Fatalf("ListTarballs: %v", err)
	}
	if len(versions) != 5 {
		t.Errorf("dry run should not delete files, got %d tarballs", len(versions))
	}
}

func TestPruneTarballsDeletesOldest(t *testing.T) {
	appName := "prune-run-app"
	for i := 1; i <= 5; i++ {
		writeLegacyTarball(t, appName, "v"+string(rune('0'+i)), 100+i)
	}

	res, err := PruneTarballs(appName, 2, nil, false)
	if err != nil {
		t.Fatalf("PruneTarballs: %v", err)
	}
	if len(res.Removed) != 3 {
		t.Fatalf("expected 3 removed, got %d", len(res.Removed))
	}
	if res.Removed[0].Version != "v1" || res.Removed[2].Version != "v3" {
		t.Errorf("expected removed [v1 v2 v3], got %v", res.Removed)
	}
	if len(res.Kept) != 2 || res.Kept[0].Version != "v4" || res.Kept[1].Version != "v5" {
		t.Errorf("expected kept [v4 v5], got %v", res.Kept)
	}
	if res.FreedBytes != (101+102+103) {
		t.Errorf("expected freed 306, got %d", res.FreedBytes)
	}

	versions, _ := ListTarballs(appName)
	if len(versions) != 2 {
		t.Errorf("expected 2 tarballs left, got %v", versions)
	}
}

func TestPruneTarballsProtectsRunningVersion(t *testing.T) {
	appName := "prune-protect-app"
	for i := 1; i <= 5; i++ {
		writeLegacyTarball(t, appName, "v"+string(rune('0'+i)), 100+i)
	}

	// v2 is the running version — must be protected even though it is old.
	res, err := PruneTarballs(appName, 2, map[string]bool{"v2": true}, false)
	if err != nil {
		t.Fatalf("PruneTarballs: %v", err)
	}
	if len(res.Protected) != 1 || res.Protected[0].Version != "v2" {
		t.Errorf("expected v2 protected, got %v", res.Protected)
	}
	// Protected tarball still on disk
	if _, err := tarballPathFor(appName, "v2"); err != nil {
		t.Errorf("v2 should still exist: %v", err)
	}
	// Oldest deleted until 2 remain in total (v2 protected + v5), so v1, v3, v4
	if len(res.Removed) != 3 {
		t.Errorf("expected 3 removed, got %d (%v)", len(res.Removed), res.Removed)
	}
	if len(res.Removed) != 3 || res.Removed[0].Version != "v1" || res.Removed[1].Version != "v3" || res.Removed[2].Version != "v4" {
		t.Errorf("expected removed [v1 v3 v4], got %v", res.Removed)
	}
	if len(res.Kept) != 1 || res.Kept[0].Version != "v5" {
		t.Errorf("expected kept [v5], got %v", res.Kept)
	}
}

func TestPruneTarballsAllProtected(t *testing.T) {
	appName := "prune-all-protected"
	writeLegacyTarball(t, appName, "v1", 100)
	writeLegacyTarball(t, appName, "v2", 200)

	// Both versions protected (running + previous) — nothing may be deleted.
	res, err := PruneTarballs(appName, 1, map[string]bool{"v1": true, "v2": true}, false)
	if err != nil {
		t.Fatalf("PruneTarballs: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Errorf("expected nothing removed, got %v", res.Removed)
	}
	if len(res.Protected) != 2 {
		t.Errorf("expected 2 protected, got %d", len(res.Protected))
	}
}

func TestPruneTarballsInvalidKeep(t *testing.T) {
	if _, err := PruneTarballs("x", 0, nil, true); err == nil {
		t.Error("expected error for keep=0")
	}
}
