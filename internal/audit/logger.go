// Package audit provides structured event logging for deploy operations.
// Entries are appended as JSONL to ~/.deploy/audit.log.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"deploy/internal/config"
)

// Entry represents a single audit log entry.
type Entry struct {
	Time       time.Time `json:"time"`
	Action     string    `json:"action"`
	App        string    `json:"app"`
	Version    string    `json:"version,omitempty"`
	InitiatedBy string   `json:"by"`
	DurationMs int64     `json:"duration_ms"`
	Result     string    `json:"result"`
}

// auditLogPath returns the path to the audit log file.
func auditLogPath() string {
	return filepath.Join(config.DeployDirPath(), "audit.log")
}

// Log appends one audit entry to ~/.deploy/audit.log.
func Log(entry Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}

	f, err := os.OpenFile(auditLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write audit entry: %w", err)
	}
	return nil
}
