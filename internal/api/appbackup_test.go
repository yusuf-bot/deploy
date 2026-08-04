package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deploy/internal/state"
	"deploy/internal/types"

	"github.com/google/uuid"
)

func writeBackupFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestPruneAppBackupsKeepN(t *testing.T) {
	dir := t.TempDir()

	// App "web": 4 archives, oldest -> newest.
	writeBackupFile(t, dir, "deploy-web-20260101-010000.tar.gz")
	writeBackupFile(t, dir, "deploy-web-20260102-010000.tar.gz")
	writeBackupFile(t, dir, "deploy-web-20260103-010000.tar.gz")
	writeBackupFile(t, dir, "deploy-web-20260104-010000.tar.gz")
	// App "api": 2 archives (must survive with keep=2).
	writeBackupFile(t, dir, "deploy-api-20260101-010000.tar.gz")
	writeBackupFile(t, dir, "deploy-api-20260105-010000.tar.gz")
	// Full-system archive + unrelated files must never be touched.
	writeBackupFile(t, dir, "deploy-20260101-010000.tar.gz")
	writeBackupFile(t, dir, "readme.txt")

	removed, err := PruneAppBackups(dir, 2)
	if err != nil {
		t.Fatalf("PruneAppBackups: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed, got %v", removed)
	}

	for _, gone := range []string{
		"deploy-web-20260101-010000.tar.gz",
		"deploy-web-20260102-010000.tar.gz",
	} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed", gone)
		}
	}
	for _, kept := range []string{
		"deploy-web-20260103-010000.tar.gz",
		"deploy-web-20260104-010000.tar.gz",
		"deploy-api-20260101-010000.tar.gz",
		"deploy-api-20260105-010000.tar.gz",
		"deploy-20260101-010000.tar.gz",
		"readme.txt",
	} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s should have been kept", kept)
		}
	}
}

func TestPruneAppBackupsNothingToRemove(t *testing.T) {
	dir := t.TempDir()
	writeBackupFile(t, dir, "deploy-web-20260101-010000.tar.gz")
	writeBackupFile(t, dir, "deploy-web-20260102-010000.tar.gz")

	removed, err := PruneAppBackups(dir, 5)
	if err != nil {
		t.Fatalf("PruneAppBackups: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("expected nothing removed, got %v", removed)
	}
}

func TestPruneAppBackupsMissingDir(t *testing.T) {
	removed, err := PruneAppBackups(filepath.Join(t.TempDir(), "nope"), 3)
	if err != nil {
		t.Fatalf("PruneAppBackups: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("expected nothing removed for missing dir, got %v", removed)
	}
}

func TestPruneAppBackupsClampsKeep(t *testing.T) {
	dir := t.TempDir()
	writeBackupFile(t, dir, "deploy-web-20260101-010000.tar.gz")
	writeBackupFile(t, dir, "deploy-web-20260102-010000.tar.gz")

	if _, err := PruneAppBackups(dir, 0); err != nil {
		t.Fatalf("PruneAppBackups: %v", err)
	}
	// keep clamped to 1 -> newest survives.
	if _, err := os.Stat(filepath.Join(dir, "deploy-web-20260102-010000.tar.gz")); err != nil {
		t.Error("newest archive should survive with keep clamped to 1")
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy-web-20260101-010000.tar.gz")); !os.IsNotExist(err) {
		t.Error("oldest archive should be removed with keep clamped to 1")
	}
}

func TestCreateAppBackupHermetic(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEPLOY_DATA_DIR", tmp)

	db, err := state.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := state.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// CreateAppBackup copies master.key into every archive.
	if err := os.WriteFile(filepath.Join(tmp, "master.key"), []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}

	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "bk-app",
		Port:  8080,
		Image: "nginx:latest",
	}
	if _, err := state.CreateApp(db, app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	path, err := CreateAppBackup(db, "bk-app")
	if err != nil {
		t.Fatalf("CreateAppBackup: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "deploy-bk-app-") || !strings.HasSuffix(path, ".tar.gz") {
		t.Fatalf("unexpected backup path %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("backup archive missing: %v", err)
	}
}
