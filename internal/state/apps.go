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

// scanAppRow scans a full app row including port.
func scanAppRow(scanner interface {
	Scan(dest ...interface{}) error
}) (types.App, error) {
	var id, name, status, image, envJSON, createdAt, updatedAt string
	var port, servicePort int
	var containerID sql.NullString

	err := scanner.Scan(&id, &name, &status, &port, &servicePort, &image, &envJSON, &containerID, &createdAt, &updatedAt)
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
		ID:        id,
		Name:      name,
		Status:    status,
		Port:        port,
		ServicePort: servicePort,
		Image:     image,
		Env:       env,
		CreatedAt: createdTime,
		UpdatedAt: updatedTime,
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

	_, err := db.Exec(
		`INSERT INTO apps (id, name, status, port, service_port, image, env, container_id, created_at, updated_at) 
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		app.ID, app.Name, app.Status, app.Port, app.ServicePort, app.Image, envJSON, containerID, now, now,
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

// GetApp retrieves an app by its ID.
func GetApp(db *sql.DB, id string) (*types.App, error) {
	row := db.QueryRow(
		`SELECT id, name, status, port, service_port, image, env, container_id, created_at, updated_at 
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
		`SELECT id, name, status, port, service_port, image, env, container_id, created_at, updated_at 
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
			`SELECT id, name, status, port, service_port, image, env, container_id, created_at, updated_at 
			 FROM apps WHERE status = ? ORDER BY name`, status,
		)
	} else {
		rows, err = db.Query(
			`SELECT id, name, status, port, service_port, image, env, container_id, created_at, updated_at 
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
