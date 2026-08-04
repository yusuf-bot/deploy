package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"deploy/internal/config"
	"deploy/internal/state"
)

// handleAppBackup creates a per-app backup archive containing the app's image
// tarball(s) plus a JSON export of its DB rows (secrets stay encrypted under
// the current master key — never plaintext) and the master key needed to
// re-encrypt them on restore. POST /api/v1/backup/{name}
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

	backupDir := filepath.Join(config.DeployDirPath(), "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(fmt.Errorf("mkdir: %w", err))))
		return
	}

	tmpDir, err := os.MkdirTemp(backupDir, ".backup-")
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(fmt.Errorf("mktemp: %w", err))))
		return
	}
	defer os.RemoveAll(tmpDir)

	// Per-app DB export (secrets remain encrypted under the master key).
	bk, err := state.ExportApp(s.db, app.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(fmt.Errorf("export app: %w", err))))
		return
	}
	data, err := json.MarshalIndent(bk, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(fmt.Errorf("marshal app.json: %w", err))))
		return
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "app.json"), data, 0600); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(fmt.Errorf("write app.json: %w", err))))
		return
	}

	// Master key: needed to decrypt + re-encrypt secrets on restore.
	if err := copyFile(filepath.Join(config.DeployDirPath(), "master.key"), filepath.Join(tmpDir, "master.key")); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(fmt.Errorf("copy master.key: %w", err))))
		return
	}

	// Image tarballs.
	srcImages := filepath.Join(config.DeployDirPath(), "images", name)
	if _, err := os.Stat(srcImages); err == nil {
		if err := copyDir(srcImages, filepath.Join(tmpDir, "images", name)); err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(systemError(fmt.Errorf("copy images: %w", err))))
			return
		}
	}

	outputPath := filepath.Join(backupDir, fmt.Sprintf("deploy-%s-%s.tar.gz", name, time.Now().Format("20060102-150405")))
	if err := tarDir(tmpDir, outputPath); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(fmt.Errorf("tar: %w", err))))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path": outputPath,
	})
}
