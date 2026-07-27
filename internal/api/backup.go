package api

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deploy/internal/config"
)

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	backupDir := filepath.Join(config.DeployDirPath(), "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody("BACKUP_FAILED", "mkdir: "+err.Error()))
		return
	}

	tmpDir, err := os.MkdirTemp(backupDir, ".backup-")
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody("BACKUP_FAILED", "mktemp: "+err.Error()))
		return
	}
	defer os.RemoveAll(tmpDir)

	// SQLite VACUUM INTO requires a string literal at parse time (not a bound parameter).
	dbPath := filepath.Join(tmpDir, "deploy.db")
	if _, err := s.db.Exec("VACUUM INTO '" + strings.ReplaceAll(dbPath, "'", "''") + "'"); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody("BACKUP_FAILED", "VACUUM INTO: "+err.Error()))
		return
	}

	// Copy other important files.
	if err := copyFile(filepath.Join(config.DeployDirPath(), "master.key"), filepath.Join(tmpDir, "master.key")); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody("BACKUP_FAILED", "copy master.key: "+err.Error()))
		return
	}

	auditLog := filepath.Join(config.DeployDirPath(), "audit.log")
	if _, err := os.Stat(auditLog); err == nil {
		if err := copyFile(auditLog, filepath.Join(tmpDir, "audit.log")); err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody("BACKUP_FAILED", "copy audit.log: "+err.Error()))
			return
		}
	}

	imagesDir := filepath.Join(config.DeployDirPath(), "images")
	if _, err := os.Stat(imagesDir); err == nil {
		if err := copyDir(imagesDir, filepath.Join(tmpDir, "images")); err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody("BACKUP_FAILED", "copy images: "+err.Error()))
			return
		}
	}

	caddyDir := filepath.Join(config.DeployDirPath(), "caddy")
	if _, err := os.Stat(caddyDir); err == nil {
		if err := copyDir(caddyDir, filepath.Join(tmpDir, "caddy")); err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody("BACKUP_FAILED", "copy caddy: "+err.Error()))
			return
		}
	}

	outputPath := filepath.Join(backupDir, fmt.Sprintf("deploy-%s.tar.gz", time.Now().Format("20060102-150405")))
	if err := tarDir(tmpDir, outputPath); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody("BACKUP_FAILED", "tar: "+err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path": outputPath,
	})
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return nil // defer out.Close() handles closing
}

// copyDir recursively copies a directory from src to dst.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0700); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// tarDir creates a tar.gz archive of the source directory at dstPath.
func tarDir(src, dstPath string) error {
	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(fi, fi.Name())
		if err != nil {
			return err
		}

		// Use relative paths inside the archive.
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		header.Name = rel

		if fi.IsDir() {
			header.Name += "/"
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !fi.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
}
