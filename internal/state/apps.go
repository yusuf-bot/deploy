package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"deploy/internal/types"
)

// serializeEnv marshals env map to JSON.
func serializeEnv(env map[string]string) string {
	if env == nil {
		return "{}"
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// scanAppRow scans a full app row including port and resource limits.
func scanAppRow(scanner interface {
	Scan(dest ...interface{}) error
}) (types.App, error) {
	var id, name, status, image, envJSON, memory, cpus, healthPath, createdAt, updatedAt string
	var port, servicePort int
	var groupID sql.NullInt64
	var containerID sql.NullString

	err := scanner.Scan(&id, &name, &status, &port, &servicePort, &groupID, &image, &envJSON, &memory, &cpus, &healthPath, &containerID, &createdAt, &updatedAt)
	if err != nil {
		return types.App{}, err
	}

	createdTime, _ := time.Parse(time.RFC3339, createdAt)
	updatedTime, _ := time.Parse(time.RFC3339, updatedAt)

	env := make(map[string]string)
	if envJSON != "" && envJSON != "{}" {
		if err := json.Unmarshal([]byte(envJSON), &env); err != nil {
			env = make(map[string]string)
		}
	}

	app := types.App{
		ID:          id,
		Name:        name,
		Status:      status,
		Port:        port,
		ServicePort: servicePort,
		HealthPath:  healthPath,
		Image:       image,
		Env:         env,
		CreatedAt:   createdTime,
		UpdatedAt:   updatedTime,
	}

	if groupID.Valid {
		gid := int(groupID.Int64)
		app.GroupID = &gid
	}

	if memory != "" || cpus != "" {
		app.Resources = &types.ResourceConfig{Memory: memory, CPUs: cpus}
	}

	if containerID.Valid {
		app.ContainerID = containerID.String
	}

	return app, nil
}

// CreateApp inserts a new app into the database.
func CreateApp(db *sql.DB, app *types.App) (*types.App, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	envJSON := serializeEnv(app.Env)

	var containerID *string
	if app.ContainerID != "" {
		containerID = &app.ContainerID
	}

	var groupID *int64
	if app.GroupID != nil {
		v := int64(*app.GroupID)
		groupID = &v
	}

	memory, cpus := serializeResources(app.Resources)

	_, err := db.Exec(
		`INSERT INTO apps (id, name, status, port, service_port, group_id, image, env, memory, cpus, container_id, created_at, updated_at) 
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		app.ID, app.Name, app.Status, app.Port, app.ServicePort, groupID, app.Image, envJSON, memory, cpus, containerID, now, now,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, fmt.Errorf("app %q already exists", app.Name)
		}
		return nil, fmt.Errorf("create app: %w", err)
	}

	app.CreatedAt, _ = time.Parse(time.RFC3339, now)
	app.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	return app, nil
}

// serializeResources flattens a ResourceConfig into its DB columns.
func serializeResources(r *types.ResourceConfig) (memory, cpus string) {
	if r == nil {
		return "", ""
	}
	return r.Memory, r.CPUs
}

// UpdateAppResources persists resource limits for an app.
func UpdateAppResources(db Execer, name string, resources *types.ResourceConfig) error {
	memory, cpus := serializeResources(resources)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE apps SET memory = ?, cpus = ?, updated_at = ? WHERE name = ?`,
		memory, cpus, now, name,
	)
	if err != nil {
		return fmt.Errorf("update app resources: %w", err)
	}
	return nil
}

// GetApp retrieves an app by its ID.
func GetApp(db *sql.DB, id string) (*types.App, error) {
	row := db.QueryRow(
		`SELECT id, name, status, port, service_port, group_id, image, env, memory, cpus, health_path, container_id, created_at, updated_at 
		 FROM apps WHERE id = ?`, id,
	)
	app, err := scanAppRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get app: %w", err)
	}
	return &app, nil
}

// GetAppByName retrieves an app by its name.
func GetAppByName(db *sql.DB, name string) (*types.App, error) {
	row := db.QueryRow(
		`SELECT id, name, status, port, service_port, group_id, image, env, memory, cpus, health_path, container_id, created_at, updated_at 
		 FROM apps WHERE name = ?`, name,
	)
	app, err := scanAppRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get app by name: %w", err)
	}
	return &app, nil
}

// ListApps retrieves all apps, optionally filtered by status.
func ListApps(db *sql.DB, status string) ([]types.App, error) {
	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = db.Query(
			`SELECT id, name, status, port, service_port, group_id, image, env, memory, cpus, health_path, container_id, created_at, updated_at 
			 FROM apps WHERE status = ? ORDER BY name`, status,
		)
	} else {
		rows, err = db.Query(
			`SELECT id, name, status, port, service_port, group_id, image, env, memory, cpus, health_path, container_id, created_at, updated_at 
			 FROM apps ORDER BY name`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	defer rows.Close()

	var apps []types.App
	for rows.Next() {
		app, err := scanAppRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan app row: %w", err)
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

// DeleteApp removes an app by its name.
func DeleteApp(db *sql.DB, name string) error {
	_, err := db.Exec(`DELETE FROM apps WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete app: %w", err)
	}
	return nil
}

// UpdateAppStatus updates the status of an app.
func UpdateAppStatus(db Execer, name string, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE apps SET status = ?, updated_at = ? WHERE name = ?`,
		status, now, name,
	)
	if err != nil {
		return fmt.Errorf("update app status: %w", err)
	}
	return nil
}

// UpdateAppContainer updates the container_id of an app.
func UpdateAppContainer(db Execer, name string, containerID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE apps SET container_id = ?, updated_at = ? WHERE name = ?`,
		containerID, now, name,
	)
	if err != nil {
		return fmt.Errorf("update app container: %w", err)
	}
	return nil
}

// SetAllRunningToUnknown sets all apps with status 'running' to 'unknown'.
func SetAllRunningToUnknown(db *sql.DB) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE apps SET status = ?, updated_at = ? WHERE status = ?`,
		types.StatusUnknown, now, types.StatusRunning,
	)
	if err != nil {
		return fmt.Errorf("reset running to unknown: %w", err)
	}
	return nil
}

// UpdateAppPort updates the port of an app.
func UpdateAppPort(db Execer, name string, port int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE apps SET port = ?, updated_at = ? WHERE name = ?`,
		port, now, name,
	)
	if err != nil {
		return fmt.Errorf("update app port: %w", err)
	}
	return nil
}

// UpdateAppGroup sets the group_id for an app.
func UpdateAppGroup(db Execer, name string, groupID *int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var g *int64
	if groupID != nil {
		v := int64(*groupID)
		g = &v
	}
	_, err := db.Exec(
		`UPDATE apps SET group_id = ?, updated_at = ? WHERE name = ?`,
		g, now, name,
	)
	if err != nil {
		return fmt.Errorf("update app group: %w", err)
	}
	return nil
}

// UpdateAppHealthPath persists the health check path for an app.
func UpdateAppHealthPath(db Execer, name string, healthPath string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE apps SET health_path = ?, updated_at = ? WHERE name = ?`,
		healthPath, now, name,
	)
	if err != nil {
		return fmt.Errorf("update app health path: %w", err)
	}
	return nil
}

// GetAppHealth retrieves health status for an app.
func GetAppHealth(db *sql.DB, appID string) (*AppHealth, error) {
	row := db.QueryRow(
		`SELECT app_id, status, last_checked, last_ok, last_error, last_notified 
		 FROM app_health WHERE app_id = ?`, appID,
	)
	var h AppHealth
	h.AppID = appID
	h.Status = "unknown"
	err := row.Scan(&h.AppID, &h.Status, &h.LastChecked, &h.LastOk, &h.LastError, &h.LastNotified)
	if err == sql.ErrNoRows {
		return &h, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get app health: %w", err)
	}
	return &h, nil
}

// UpdateAppHealth upserts health status for an app.
func UpdateAppHealth(db Execer, appID, status, lastChecked, lastOk, lastError, lastNotified string) error {
	_, err := db.Exec(
		`INSERT INTO app_health (app_id, status, last_checked, last_ok, last_error, last_notified)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(app_id) DO UPDATE SET status=excluded.status, last_checked=excluded.last_checked, 
		 last_ok=excluded.last_ok, last_error=excluded.last_error, last_notified=excluded.last_notified`,
		appID, status, lastChecked, lastOk, lastError, lastNotified,
	)
	if err != nil {
		return fmt.Errorf("update app health: %w", err)
	}
	return nil
}

// AppHealth represents the health status of an app.
type AppHealth struct {
	AppID        string `json:"app_id"`
	Status       string `json:"status"`
	LastChecked  string `json:"last_checked"`
	LastOk       string `json:"last_ok"`
	LastError    string `json:"last_error"`
	LastNotified string `json:"last_notified"`
}

// UpdateAppServicePort updates the container (service) port of an app.
// servicePort is the port the app listens on inside the container; 0 means the
// container port equals the host port (app.Port).
func UpdateAppServicePort(db Execer, name string, servicePort int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE apps SET service_port = ?, updated_at = ? WHERE name = ?`,
		servicePort, now, name,
	)
	if err != nil {
		return fmt.Errorf("update app service port: %w", err)
	}
	return nil
}

// ListAssignedPorts returns all currently assigned ports from the apps table.
func ListAssignedPorts(db *sql.DB) ([]int, error) {
	rows, err := db.Query("SELECT port FROM apps")
	if err != nil {
		return nil, fmt.Errorf("list assigned ports: %w", err)
	}
	defer rows.Close()
	var ports []int
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan port: %w", err)
		}
		ports = append(ports, p)
	}
	return ports, rows.Err()
}
