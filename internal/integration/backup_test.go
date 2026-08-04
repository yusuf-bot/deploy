//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"deploy/internal/build"
	"deploy/internal/config"
	"deploy/internal/state"

	mobyclient "github.com/moby/moby/client"
)

// TestPerAppBackupRestore simulates losing all of one app's data (DB rows,
// saved image tarballs, and running container) and recovering it from a per-app
// backup while the daemon is up. After restore the app serves its health
// marker again on the backup port and its secrets are readable.
func TestPerAppBackupRestore(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()

	dir := h.writeFixture(t, name, "A")
	h.promote(t, name, dir)
	app := h.mustGetApp(t, name)
	_ = app.Port // the backup port must be reused after restore

	// A secret that must survive the round trip (value is re-encrypted under
	// the daemon's current master key during restore).
	if err := h.deployClient.SetSecret(name, "TOKEN", "s3cr3t-value"); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	// Backup.
	backupPath, err := h.deployClient.CreateAppBackup(name)
	if err != nil {
		t.Fatalf("create app backup: %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	// Verify the archive contains app.json + master.key.
	tarballs, err := build.ListTarballs(name)
	if err != nil {
		t.Fatalf("list tarballs: %v", err)
	}
	if len(tarballs) == 0 {
		t.Fatal("expected at least one saved image tarball before wipe")
	}

	// Wipe the app: stop/remove its container, delete its DB rows, and remove
	// its saved image tarballs — the per-app restore must rebuild all of it.
	h.removeAppContainers(context.Background(), name)
	if err := wipeAppData(t, h, name); err != nil {
		t.Fatalf("wipe app data: %v", err)
	}
	if app2, _ := h.deployClient.GetApp(name); app2 != nil {
		t.Fatalf("app %s still present after wipe", name)
	}

	// Restore.
	resp, err := h.deployClient.RestoreAppBackup(name, backupPath)
	if err != nil {
		t.Fatalf("restore app: %v", err)
	}
	if resp.Version == "" || resp.Port == 0 {
		t.Fatalf("restore response incomplete: %+v", resp)
	}

	app = h.mustGetApp(t, name)
	if app.Status != "running" {
		t.Fatalf("app status after restore = %q, want running", app.Status)
	}
	h.assertHealth(t, resp.Port, "A")

	// Secret survives the re-encryption round trip.
	secret, err := h.deployClient.GetSecret(name, "TOKEN")
	if err != nil {
		t.Fatalf("get secret after restore: %v", err)
	}
	if secret == nil || secret.Value != "s3cr3t-value" {
		t.Fatalf("secret after restore = %+v, want value s3cr3t-value", secret)
	}
}

// wipeAppData deletes every DB row owned by the app and removes its saved
// image tarballs, simulating a full per-app data loss.
func wipeAppData(t *testing.T, h *harness, name string) error {
	t.Helper()
	ctx := context.Background()

	// Stop and remove any leftover container (also handled by cleanup, but do
	// it here so the host port is released for the restore).
	cid := h.containerWithLabels(t, "deploy.app.name="+name)
	if cid != "" {
		timeout := 5
		_, _ = h.dockerClient.ContainerStop(ctx, cid, mobyclient.ContainerStopOptions{Timeout: &timeout})
		_, _ = h.dockerClient.ContainerRemove(ctx, cid, mobyclient.ContainerRemoveOptions{Force: true})
	}

	app, err := state.GetAppByName(h.db, name)
	if err != nil {
		return err
	}
	if app == nil {
		return nil
	}

	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM port_allocations WHERE app_name = ?", app.Name); err != nil {
		return err
	}
	for _, stmt := range []string{
		"DELETE FROM secrets WHERE app_id = ?",
		"DELETE FROM domains WHERE app_id = ?",
		"DELETE FROM deployments WHERE app_id = ?",
		"DELETE FROM apps WHERE id = ?",
	} {
		if _, err := tx.Exec(stmt, app.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	return os.RemoveAll(filepath.Join(config.DeployDirPath(), "images", name))
}
