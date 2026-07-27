package state

import (
	"database/sql"
	"fmt"
	"time"

	"deploy/internal/types"
)

// CreateDeployment inserts a new deployment record.
func CreateDeployment(db *sql.DB, d *types.Deployment) (*types.Deployment, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var imageDigest, oldContainerID, newContainerID, deployLog *string
	if d.ImageDigest != "" {
		imageDigest = &d.ImageDigest
	}
	if d.OldContainerID != "" {
		oldContainerID = &d.OldContainerID
	}
	if d.NewContainerID != "" {
		newContainerID = &d.NewContainerID
	}
	if d.DeployLog != "" {
		deployLog = &d.DeployLog
	}

	_, err := db.Exec(
		`INSERT INTO deployments (id, app_id, version, image_digest, status, old_container_id, new_container_id, port, deploy_log, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.AppID, d.Version, imageDigest, d.Status, oldContainerID, newContainerID, d.Port, deployLog, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}

	d.CreatedAt, _ = time.Parse(time.RFC3339, now)
	d.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	return d, nil
}

// scanDeploymentRow scans a single deployment row from a scanner.
func scanDeploymentRow(scanner interface {
	Scan(dest ...interface{}) error
}) (types.Deployment, error) {
	var id, appID, version, status, createdAt, updatedAt string
	var imageDigest, oldContainerID, newContainerID, deployLog sql.NullString
	var port sql.NullInt64

	err := scanner.Scan(&id, &appID, &version, &imageDigest, &status, &oldContainerID, &newContainerID, &port, &deployLog, &createdAt, &updatedAt)
	if err != nil {
		return types.Deployment{}, err
	}

	dep := types.Deployment{
		ID:      id,
		AppID:   appID,
		Version: version,
		Status:  status,
	}
	if imageDigest.Valid {
		dep.ImageDigest = imageDigest.String
	}
	if oldContainerID.Valid {
		dep.OldContainerID = oldContainerID.String
	}
	if newContainerID.Valid {
		dep.NewContainerID = newContainerID.String
	}
	if port.Valid {
		dep.Port = int(port.Int64)
	}
	if deployLog.Valid {
		dep.DeployLog = deployLog.String
	}
	dep.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	dep.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return dep, nil
}

// GetDeployment retrieves a deployment by its ID.
func GetDeployment(db *sql.DB, id string) (*types.Deployment, error) {
	row := db.QueryRow(
		`SELECT id, app_id, version, image_digest, status, old_container_id, new_container_id, port, deploy_log, created_at, updated_at
		 FROM deployments WHERE id = ?`, id,
	)
	dep, err := scanDeploymentRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	return &dep, nil
}

// ListDeploymentsByApp returns all deployments for an app, newest first.
func ListDeploymentsByApp(db *sql.DB, appID string) ([]types.Deployment, error) {
	rows, err := db.Query(
		`SELECT id, app_id, version, image_digest, status, old_container_id, new_container_id, port, deploy_log, created_at, updated_at
		 FROM deployments WHERE app_id = ? ORDER BY created_at DESC`, appID,
	)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var deps []types.Deployment
	for rows.Next() {
		dep, err := scanDeploymentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deployment row: %w", err)
		}
		deps = append(deps, dep)
	}
	return deps, rows.Err()
}

// UpdateDeploymentStatus updates the status and deploy_log of a deployment.
func UpdateDeploymentStatus(db *sql.DB, id string, status string, deployLog string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	var logArg *string
	if deployLog != "" {
		logArg = &deployLog
	}

	res, err := db.Exec(
		`UPDATE deployments SET status = ?, deploy_log = ?, updated_at = ? WHERE id = ?`,
		status, logArg, now, id,
	)
	if err != nil {
		return fmt.Errorf("update deployment status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("deployment %q not found", id)
	}
	return nil
}

// GetActiveDeployment returns the active deployment for an app, if any.
func GetActiveDeployment(db *sql.DB, appID string) (*types.Deployment, error) {
	row := db.QueryRow(
		`SELECT id, app_id, version, image_digest, status, old_container_id, new_container_id, port, deploy_log, created_at, updated_at
		 FROM deployments WHERE app_id = ? AND status = ? ORDER BY created_at DESC LIMIT 1`,
		appID, types.DeployStatusActive,
	)
	dep, err := scanDeploymentRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active deployment: %w", err)
	}
	return &dep, nil
}

// SetAllDeployingToFailed marks all 'deploying' deployments as 'failed'.
// Used on daemon restart to recover from in-flight deployments.
func SetAllDeployingToFailed(db *sql.DB) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(
		`UPDATE deployments SET status = ?, deploy_log = ?, updated_at = ? WHERE status = ?`,
		types.DeployStatusFailed, "Daemon restarted while deploying", now, types.DeployStatusDeploying,
	)
	if err != nil {
		return fmt.Errorf("set deploying to failed: %w", err)
	}
	_, _ = res.RowsAffected() // not needed, call is best-effort
	return nil
}

// SetActiveDeployment marks a deployment as the active one for its app.
func SetActiveDeployment(db *sql.DB, d *types.Deployment) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE deployments SET status = ?, updated_at = ? WHERE id = ? AND app_id = ?`,
		types.DeployStatusActive, now, d.ID, d.AppID,
	)
	if err != nil {
		return fmt.Errorf("set active deployment: %w", err)
	}
	return nil
}

// DeactivateOtherDeployments sets all deployments for an app (except the given ID) to 'inactive'.
func DeactivateOtherDeployments(db *sql.DB, appID string, excludeID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE deployments SET status = ?, updated_at = ? WHERE app_id = ? AND id != ? AND status = ?`,
		types.DeployStatusInactive, now, appID, excludeID, types.DeployStatusActive,
	)
	if err != nil {
		return fmt.Errorf("deactivate other deployments: %w", err)
	}
	return nil
}
