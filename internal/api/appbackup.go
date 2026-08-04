package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"deploy/internal/config"
	"deploy/internal/state"
)

// CreateAppBackup creates a per-app backup archive for name — the same
// server-side code path as POST /api/v1/backup/{name} — and returns the path
// to the archive. It contains the app's image tarball(s) plus a JSON export of
// its DB rows (secrets stay encrypted under the current master key — never
// plaintext) and the master key needed to re-encrypt them on restore.
func CreateAppBackup(db *sql.DB, name string) (string, error) {
	// validAppName is defined in handlers.go (same package).
	if name == "" || !validAppName.MatchString(name) {
		return "", fmt.Errorf("invalid app name %q", name)
	}

	app, err := state.GetAppByName(db, name)
	if err != nil {
		return "", err
	}
	if app == nil {
		return "", fmt.Errorf("app %q not found", name)
	}

	backupDir := filepath.Join(config.DeployDirPath(), "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	tmpDir, err := os.MkdirTemp(backupDir, ".backup-")
	if err != nil {
		return "", fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Per-app DB export (secrets remain encrypted under the master key).
	bk, err := state.ExportApp(db, app.ID)
	if err != nil {
		return "", fmt.Errorf("export app: %w", err)
	}
	data, err := json.MarshalIndent(bk, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal app.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "app.json"), data, 0600); err != nil {
		return "", fmt.Errorf("write app.json: %w", err)
	}

	// Master key: needed to decrypt + re-encrypt secrets on restore.
	if err := copyFile(filepath.Join(config.DeployDirPath(), "master.key"), filepath.Join(tmpDir, "master.key")); err != nil {
		return "", fmt.Errorf("copy master.key: %w", err)
	}

	// Image tarballs.
	srcImages := filepath.Join(config.DeployDirPath(), "images", name)
	if _, err := os.Stat(srcImages); err == nil {
		if err := copyDir(srcImages, filepath.Join(tmpDir, "images", name)); err != nil {
			return "", fmt.Errorf("copy images: %w", err)
		}
	}

	outputPath := filepath.Join(backupDir, fmt.Sprintf("deploy-%s-%s.tar.gz", name, time.Now().Format("20060102-150405")))
	if err := tarDir(tmpDir, outputPath); err != nil {
		return "", fmt.Errorf("tar: %w", err)
	}

	return outputPath, nil
}

// handleAppBackup creates a per-app backup archive for one app.
// POST /api/v1/backup/{name}
func (s *Server) handleAppBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || !validAppName.MatchString(name) {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid app name"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	outputPath, err := CreateAppBackup(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path": outputPath,
	})
}

// backupArchiveTSRe matches the timestamp segment (YYYYMMDD-HHMMSS) embedded
// in per-app backup archive filenames.
var backupArchiveTSRe = regexp.MustCompile(`^\d{8}-\d{6}$`)

// parseAppBackupName splits a backup archive filename into the app name and
// its embedded timestamp. Per-app archives are named
// deploy-<app>-<YYYYMMDD-HHMMSS>.tar.gz; full-system archives
// (deploy-<ts>.tar.gz) and unrelated files are rejected.
func parseAppBackupName(base string) (app, ts string, ok bool) {
	rest, found := strings.CutPrefix(base, "deploy-")
	if !found {
		return "", "", false
	}
	rest, found = strings.CutSuffix(rest, ".tar.gz")
	if !found {
		return "", "", false
	}
	// rest must be <app> "-" <ts> with ts fixed at 15 chars.
	if len(rest) < 17 {
		return "", "", false
	}
	ts = rest[len(rest)-15:]
	if !backupArchiveTSRe.MatchString(ts) {
		return "", "", false
	}
	return rest[:len(rest)-16], ts, true
}

// PruneAppBackups enforces backup retention: it keeps the newest keep per-app
// backup archives per app in backupDir and deletes the rest. Full-system
// archives (deploy-<ts>.tar.gz) and unrelated files are never touched. This
// mirrors the `deploy prune` semantics (keep newest N, delete older). It
// returns the paths of the deleted archives.
func PruneAppBackups(backupDir string, keep int) ([]string, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if keep < 1 {
		keep = 1
	}

	byApp := make(map[string][]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		app, _, ok := parseAppBackupName(e.Name())
		if !ok {
			continue
		}
		byApp[app] = append(byApp[app], e.Name())
	}

	var removed []string
	for _, files := range byApp {
		// Timestamps sort lexicographically within a fixed-width YYYYMMDD-HHMMSS.
		sort.Strings(files)
		if len(files) <= keep {
			continue
		}
		for _, f := range files[:len(files)-keep] {
			p := filepath.Join(backupDir, f)
			if err := os.Remove(p); err != nil {
				return removed, err
			}
			removed = append(removed, p)
		}
	}
	return removed, nil
}
