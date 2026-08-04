package state

import (
	"database/sql"
	"fmt"
	"time"
)

// Migration represents a single database schema migration.
type Migration struct {
	Version int
	SQL     string
}

// migrations holds all schema migrations in order.
// v1 = Phase 1 schema (apps + jobs tables).
// v2 = Phase 2 schema (deployments + secrets tables).
// v3 = Phase 3 schema (domains table).
// v4 = Phase 4 schema (settings table). (domains table).
// v5 = Phase 5 schema (port_allocations table).
// v6 = http_only flag on domains (serve domain HTTP-only, no TLS/https block).
// v7 = service_port on apps (container port for host->container bindings).
// v8 = memory/cpus resource limits on apps (persisted from deploy.yml).
var migrations = []Migration{
	{
		Version: 1,
		SQL: `
			CREATE TABLE IF NOT EXISTS apps (
				id          TEXT PRIMARY KEY,
				name        TEXT UNIQUE NOT NULL,
				status      TEXT NOT NULL DEFAULT 'created',
				port        INTEGER NOT NULL,
				image       TEXT NOT NULL,
				env         TEXT NOT NULL DEFAULT '{}',
				container_id TEXT,
				created_at  TEXT NOT NULL,
				updated_at  TEXT NOT NULL
			);
			CREATE TABLE IF NOT EXISTS jobs (
				id          TEXT PRIMARY KEY,
				type        TEXT NOT NULL,
				status      TEXT NOT NULL DEFAULT 'done',
				app_id      TEXT REFERENCES apps(id) ON DELETE CASCADE,
				result      TEXT,
				error       TEXT,
				created_at  TEXT NOT NULL,
				completed_at TEXT
			);
			CREATE INDEX IF NOT EXISTS idx_apps_name ON apps(name);
			CREATE INDEX IF NOT EXISTS idx_apps_status ON apps(status);
			CREATE INDEX IF NOT EXISTS idx_jobs_app_id ON jobs(app_id);
		`,
	},
	{
		Version: 2,
		SQL: `
			CREATE TABLE IF NOT EXISTS deployments (
				id TEXT PRIMARY KEY,
				app_id TEXT NOT NULL REFERENCES apps(id),
				version TEXT NOT NULL,
				image_digest TEXT,
				status TEXT NOT NULL DEFAULT 'pending',
				old_container_id TEXT,
				new_container_id TEXT,
				port INTEGER,
				deploy_log TEXT,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);
			CREATE TABLE IF NOT EXISTS secrets (
				app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
				key TEXT NOT NULL,
				value TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				PRIMARY KEY (app_id, key)
			);
			CREATE INDEX IF NOT EXISTS idx_deployments_app_id ON deployments(app_id);
			CREATE INDEX IF NOT EXISTS idx_deployments_status ON deployments(status);
		`,
	},
	{
		Version: 3,
		SQL: `
			CREATE TABLE IF NOT EXISTS domains (
				id         TEXT PRIMARY KEY,
				app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
				domain     TEXT UNIQUE NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_domains_app_id ON domains(app_id);
			CREATE INDEX IF NOT EXISTS idx_domains_domain ON domains(domain);
		`,
	},
	{
		Version: 4,
		SQL: `
			CREATE TABLE IF NOT EXISTS settings (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL
			);
		`,
	},
	{
		Version: 5,
		SQL: `
			CREATE TABLE IF NOT EXISTS port_allocations (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				app_name TEXT NOT NULL UNIQUE,
				port INTEGER NOT NULL UNIQUE,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);
			INSERT OR IGNORE INTO port_allocations (app_name, port) SELECT name, port FROM apps;
		`,
	},
	{
		Version: 6,
		SQL: `
			ALTER TABLE domains ADD COLUMN http_only INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		Version: 7,
		SQL: `
			ALTER TABLE apps ADD COLUMN service_port INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		Version: 8,
		SQL: `
			ALTER TABLE apps ADD COLUMN memory TEXT NOT NULL DEFAULT '';
			ALTER TABLE apps ADD COLUMN cpus TEXT NOT NULL DEFAULT '';
		`,
	},
	{
		Version: 9,
		SQL: `
			CREATE TABLE IF NOT EXISTS env_groups (
				id INTEGER PRIMARY KEY,
				name TEXT UNIQUE NOT NULL,
				client TEXT NOT NULL DEFAULT '',
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS env_group_vars (
				group_id INTEGER NOT NULL REFERENCES env_groups(id) ON DELETE CASCADE,
				key TEXT NOT NULL,
				value TEXT NOT NULL,
				PRIMARY KEY (group_id, key)
			);
			ALTER TABLE apps ADD COLUMN group_id INTEGER REFERENCES env_groups(id);
		`,
	},
}

// EnsureSchemaMigrationsTable creates the tracking table if it doesn't exist.
func EnsureSchemaMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// isMigrationApplied checks whether a given migration version has been recorded.
func isMigrationApplied(db *sql.DB, version int) (bool, error) {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}
	return count > 0, nil
}

// recordMigration inserts a migration version as applied.
func recordMigration(db *sql.DB, version int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		"INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
		version, now,
	)
	if err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	return nil
}

// RunMigrations runs all pending migrations in order.
// Idempotent — safe to call on every daemon start.
func RunMigrations(db *sql.DB) error {
	if err := EnsureSchemaMigrationsTable(db); err != nil {
		return err
	}

	for _, m := range migrations {
		applied, err := isMigrationApplied(db, m.Version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		if _, err := db.Exec(m.SQL); err != nil {
			return fmt.Errorf("run migration v%d: %w", m.Version, err)
		}

		if err := recordMigration(db, m.Version); err != nil {
			return err
		}
	}

	return nil
}
