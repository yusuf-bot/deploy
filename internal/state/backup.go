package state

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"deploy/internal/types"

	"github.com/google/uuid"
)

// AppBackupData is the JSON payload describing one app's state inside a
// per-app backup archive (app.json). Secrets are stored with their raw
// encrypted values (encrypted under the archive's master key) — never
// plaintext.
type AppBackupData struct {
	App            types.App         `json:"app"`
	Deployments    []types.Deployment `json:"deployments"`
	Secrets        []SecretBackupRow `json:"secrets"`
	Domains        []*types.Domain   `json:"domains"`
	PortAllocation int               `json:"port"`
}

// SecretBackupRow is a secret row exported with its stored (encrypted) value.
type SecretBackupRow struct {
	Key       string `json:"key"`
	Value     string `json:"value"` // encrypted with the archive's master key
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ExportApp serializes all of an app's DB rows into a backup payload. Secret
// values are exported as-is (still encrypted under the master key).
func ExportApp(db *sql.DB, appID string) (*AppBackupData, error) {
	app, err := GetApp(db, appID)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, fmt.Errorf("app %q not found", appID)
	}

	deps, err := ListDeploymentsByApp(db, appID)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}

	rows, err := db.Query(
		`SELECT key, value, created_at, updated_at FROM secrets WHERE app_id = ? ORDER BY key`, appID,
	)
	if err != nil {
		return nil, fmt.Errorf("list secrets for backup: %w", err)
	}
	defer rows.Close()

	var secrets []SecretBackupRow
	for rows.Next() {
		var s SecretBackupRow
		if err := rows.Scan(&s.Key, &s.Value, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan secret row: %w", err)
		}
		secrets = append(secrets, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	domains, err := ListDomainsByApp(db, appID)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}

	port := 0
	if err := db.QueryRow("SELECT port FROM port_allocations WHERE app_name = ?", app.Name).Scan(&port); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("get port allocation: %w", err)
	}

	return &AppBackupData{
		App:            *app,
		Deployments:    deps,
		Secrets:        secrets,
		Domains:        domains,
		PortAllocation: port,
	}, nil
}

// ImportApp restores an app backup payload into the DB in FK-safe order
// (app -> port_allocations -> secrets -> domains -> deployments) and is
// idempotent on conflict: an existing app row is kept (its ID is adopted for
// all dependent rows) and dependent rows are inserted with OR IGNORE / upsert
// semantics. Secret values are decrypted with oldKey (the backup's master key)
// and re-encrypted with newKey (the current daemon key). Returns the resolved
// app ID.
func ImportApp(db *sql.DB, bk *AppBackupData, oldKey, newKey []byte) (string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// 1. app row — keep an existing row on conflict (idempotent).
	var appID string
	err = tx.QueryRow("SELECT id FROM apps WHERE name = ?", bk.App.Name).Scan(&appID)
	if err == sql.ErrNoRows {
		app := bk.App
		app.ContainerID = ""
		envJSON := serializeEnv(app.Env)
		memory, cpus := serializeResources(app.Resources)
		now := time.Now().UTC().Format(time.RFC3339)
		_, insErr := tx.Exec(
			`INSERT INTO apps (id, name, status, port, service_port, image, env, memory, cpus, container_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			app.ID, app.Name, app.Status, app.Port, app.ServicePort, app.Image,
			envJSON, memory, cpus, nil, now, now,
		)
		if insErr != nil {
			if strings.Contains(insErr.Error(), "UNIQUE constraint") {
				// Race: created by another goroutine — adopt that row.
				if err := tx.QueryRow("SELECT id FROM apps WHERE name = ?", app.Name).Scan(&appID); err != nil {
					return "", fmt.Errorf("lookup app after race: %w", err)
				}
			} else {
				return "", fmt.Errorf("create app: %w", insErr)
			}
		} else {
			appID = app.ID
		}
	} else if err != nil {
		return "", fmt.Errorf("lookup app: %w", err)
	}

	// 2. port allocation (unique on app_name).
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO port_allocations (app_name, port) VALUES (?, ?)`,
		bk.App.Name, bk.PortAllocation,
	); err != nil {
		return "", fmt.Errorf("restore port allocation: %w", err)
	}

	// 3. secrets — decrypt with the backup key, re-encrypt with the new key.
	for _, s := range bk.Secrets {
		if s.Key == "" {
			continue
		}
		plain, decErr := DecryptSecret(s.Value, oldKey)
		if decErr != nil {
			return "", fmt.Errorf("decrypt secret %q from backup: %w", s.Key, decErr)
		}
		enc, encErr := EncryptSecret(plain, newKey)
		if encErr != nil {
			return "", fmt.Errorf("re-encrypt secret %q: %w", s.Key, encErr)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		createdAt, updatedAt := s.CreatedAt, s.UpdatedAt
		if createdAt == "" {
			createdAt = now
		}
		if updatedAt == "" {
			updatedAt = now
		}
		if _, err := tx.Exec(
			`INSERT INTO secrets (app_id, key, value, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(app_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			appID, s.Key, enc, createdAt, updatedAt,
		); err != nil {
			return "", fmt.Errorf("restore secret %q: %w", s.Key, err)
		}
	}

	// 4. domains (unique on id and domain).
	for _, d := range bk.Domains {
		if d == nil {
			continue
		}
		if d.Domain == "" {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339)
		id := d.ID
		if id == "" {
			id = uuid.New().String()
		}
		createdAt, updatedAt := d.CreatedAt, d.UpdatedAt
		if createdAt == "" {
			createdAt = now
		}
		if updatedAt == "" {
			updatedAt = now
		}
		httpOnly := 0
		if d.HTTPOnly {
			httpOnly = 1
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO domains (id, app_id, domain, http_only, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, appID, d.Domain, httpOnly, createdAt, updatedAt,
		); err != nil {
			return "", fmt.Errorf("restore domain %q: %w", d.Domain, err)
		}
	}

	// 5. deployments (unique on id).
	for _, dep := range bk.Deployments {
		if dep.Version == "" {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339)
		id := dep.ID
		if id == "" {
			id = uuid.New().String()
		}
		var imageDigest, oldCID, newCID, deployLog *string
		if dep.ImageDigest != "" {
			imageDigest = &dep.ImageDigest
		}
		if dep.OldContainerID != "" {
			oldCID = &dep.OldContainerID
		}
		if dep.NewContainerID != "" {
			newCID = &dep.NewContainerID
		}
		if dep.DeployLog != "" {
			deployLog = &dep.DeployLog
		}
		createdAt := dep.CreatedAt.UTC().Format(time.RFC3339)
		if createdAt == "0001-01-01T00:00:00Z" {
			createdAt = now
		}
		updatedAt := dep.UpdatedAt.UTC().Format(time.RFC3339)
		if updatedAt == "0001-01-01T00:00:00Z" {
			updatedAt = now
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO deployments (id, app_id, version, image_digest, status, old_container_id, new_container_id, port, deploy_log, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, appID, dep.Version, imageDigest, dep.Status, oldCID, newCID, dep.Port, deployLog, createdAt, updatedAt,
		); err != nil {
			return "", fmt.Errorf("restore deployment %q: %w", dep.Version, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit import: %w", err)
	}
	return appID, nil
}
