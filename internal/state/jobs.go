package state

import (
	"database/sql"
	"fmt"
	"time"

	"deploy/internal/types"
)

// CreateJob inserts a completed job into the database.
func CreateJob(db *sql.DB, job *types.Job) error {
	now := job.CreatedAt.Format(time.RFC3339)
	var completedAt *string
	if job.CompletedAt != nil {
		s := job.CompletedAt.Format(time.RFC3339)
		completedAt = &s
	}

	var result, errStr, appID *string
	if job.Result != "" {
		result = &job.Result
	}
	if job.Error != "" {
		errStr = &job.Error
	}
	if job.AppID != "" {
		appID = &job.AppID
	}

	_, dbErr := db.Exec(
		`INSERT INTO jobs (id, type, status, app_id, result, error, created_at, completed_at) 
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Type, job.Status, appID, result, errStr, now, completedAt,
	)
	if dbErr != nil {
		return fmt.Errorf("create job: %w", dbErr)
	}
	return nil
}

// GetJob retrieves a job by its ID.
func GetJob(db *sql.DB, id string) (*types.Job, error) {
	row := db.QueryRow(
		`SELECT id, type, status, app_id, result, error, created_at, completed_at 
		 FROM jobs WHERE id = ?`, id,
	)

	var jobID, jType, status, createdAt string
	var appID, result, errorStr, completedAt sql.NullString

	err := row.Scan(&jobID, &jType, &status, &appID, &result, &errorStr, &createdAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}

	job := types.Job{
		ID:     jobID,
		Type:   jType,
		Status: status,
	}

	if appID.Valid {
		job.AppID = appID.String
	}
	if result.Valid {
		job.Result = result.String
	}
	if errorStr.Valid {
		job.Error = errorStr.String
	}

	job.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339, completedAt.String)
		job.CompletedAt = &t
	}

	return &job, nil
}
