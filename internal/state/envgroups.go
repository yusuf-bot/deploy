package state

import (
	"database/sql"
	"fmt"
	"time"
)

type EnvGroup struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Client    string    `json:"client"`
	CreatedAt time.Time `json:"created_at"`
}

type EnvGroupVar struct {
	GroupID int    `json:"group_id"`
	Key     string `json:"key"`
	Value   string `json:"value"`
}

func CreateEnvGroup(db *sql.DB, name, client string) (*EnvGroup, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var id int64
	err := db.QueryRow(
		`INSERT INTO env_groups (name, client, created_at) VALUES (?, ?, ?) RETURNING id`,
		name, client, now,
	).Scan(&id)
	if err != nil {
		if sqlErr, ok := err.(interface{ Error() string }); ok && containsUniqueConstraint(sqlErr.Error()) {
			return nil, fmt.Errorf("env group %q already exists", name)
		}
		return nil, fmt.Errorf("create env group: %w", err)
	}
	return GetEnvGroupByID(db, int(id))
}

func containsUniqueConstraint(err string) bool {
	return len(err) > 0 && (contains(err, "UNIQUE constraint") || contains(err, "unique constraint"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func GetEnvGroupByID(db *sql.DB, id int) (*EnvGroup, error) {
	row := db.QueryRow(
		`SELECT id, name, client, created_at FROM env_groups WHERE id = ?`, id,
	)
	var g EnvGroup
	var createdAt string
	err := row.Scan(&g.ID, &g.Name, &g.Client, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get env group by id: %w", err)
	}
	g.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &g, nil
}

func GetEnvGroupByName(db *sql.DB, name string) (*EnvGroup, error) {
	row := db.QueryRow(
		`SELECT id, name, client, created_at FROM env_groups WHERE name = ?`, name,
	)
	var g EnvGroup
	var createdAt string
	err := row.Scan(&g.ID, &g.Name, &g.Client, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get env group by name: %w", err)
	}
	g.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &g, nil
}

func ListEnvGroups(db *sql.DB) ([]EnvGroup, error) {
	rows, err := db.Query(`SELECT id, name, client, created_at FROM env_groups ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list env groups: %w", err)
	}
	defer rows.Close()

	var groups []EnvGroup
	for rows.Next() {
		var g EnvGroup
		var createdAt string
		if err := rows.Scan(&g.ID, &g.Name, &g.Client, &createdAt); err != nil {
			return nil, fmt.Errorf("scan env group: %w", err)
		}
		g.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func DeleteEnvGroup(db *sql.DB, id int) error {
	res, err := db.Exec(`DELETE FROM env_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete env group: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("env group %d not found", id)
	}
	return nil
}

func SetEnvGroupVar(db *sql.DB, groupID int, key, value string) error {
	_, err := db.Exec(
		`INSERT INTO env_group_vars (group_id, key, value) VALUES (?, ?, ?)
		 ON CONFLICT(group_id, key) DO UPDATE SET value = excluded.value`,
		groupID, key, value,
	)
	if err != nil {
		return fmt.Errorf("set env group var: %w", err)
	}
	return nil
}

func GetEnvGroupVars(db *sql.DB, groupID int) (map[string]string, error) {
	rows, err := db.Query(
		`SELECT key, value FROM env_group_vars WHERE group_id = ? ORDER BY key`, groupID,
	)
	if err != nil {
		return nil, fmt.Errorf("get env group vars: %w", err)
	}
	defer rows.Close()

	vars := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan env group var: %w", err)
		}
		vars[k] = v
	}
	return vars, rows.Err()
}

func DeleteEnvGroupVar(db *sql.DB, groupID int, key string) error {
	res, err := db.Exec(
		`DELETE FROM env_group_vars WHERE group_id = ? AND key = ?`, groupID, key,
	)
	if err != nil {
		return fmt.Errorf("delete env group var: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("env group var %q not found in group %d", key, groupID)
	}
	return nil
}

func SetAppGroup(db *sql.DB, appName string, groupID *int) error {
	return UpdateAppGroup(db, appName, groupID)
}

func ClearAppGroup(db *sql.DB, appName string) error {
	return UpdateAppGroup(db, appName, nil)
}

func GetAppGroup(db *sql.DB, appName string) (*EnvGroup, error) {
	app, err := GetAppByName(db, appName)
	if err != nil {
		return nil, err
	}
	if app == nil || app.GroupID == nil {
		return nil, nil
	}
	return GetEnvGroupByID(db, *app.GroupID)
}

func GetGroupEnv(db *sql.DB, groupID int) (map[string]string, error) {
	return GetEnvGroupVars(db, groupID)
}