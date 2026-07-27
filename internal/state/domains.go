package state

import (
	"database/sql"
	"fmt"
	"time"

	"deploy/internal/types"

	"github.com/google/uuid"
)

// scanDomainRow scans a domain row from a scanner.
func scanDomainRow(scanner interface {
	Scan(dest ...interface{}) error
}) (types.Domain, error) {
	var id, appID, domain, createdAt, updatedAt string
	err := scanner.Scan(&id, &appID, &domain, &createdAt, &updatedAt)
	if err != nil {
		return types.Domain{}, err
	}
	return types.Domain{
		ID:        id,
		AppID:     appID,
		Domain:    domain,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

// CreateDomain inserts a new domain into the database.
// The ID and timestamps are set on the passed domain struct.
func CreateDomain(db *sql.DB, domain *types.Domain) error {
	if domain.Domain == "" {
		return fmt.Errorf("domain must not be empty")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	domain.ID = uuid.New().String()
	domain.CreatedAt = now
	domain.UpdatedAt = now

	_, err := db.Exec(
		`INSERT INTO domains (id, app_id, domain, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		domain.ID, domain.AppID, domain.Domain, now, now,
	)
	if err != nil {
		return fmt.Errorf("create domain: %w", err)
	}
	return nil
}

// GetDomain retrieves a domain by its ID.
func GetDomain(db *sql.DB, id string) (*types.Domain, error) {
	row := db.QueryRow(
		`SELECT id, app_id, domain, created_at, updated_at
		 FROM domains WHERE id = ?`, id,
	)
	d, err := scanDomainRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	return &d, nil
}

// GetDomainByDomain retrieves a domain by its domain name.
func GetDomainByDomain(db *sql.DB, domain string) (*types.Domain, error) {
	row := db.QueryRow(
		`SELECT id, app_id, domain, created_at, updated_at
		 FROM domains WHERE domain = ?`, domain,
	)
	d, err := scanDomainRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get domain by domain: %w", err)
	}
	return &d, nil
}

// ListDomains retrieves all domains.
func ListDomains(db *sql.DB) ([]*types.Domain, error) {
	rows, err := db.Query(
		`SELECT id, app_id, domain, created_at, updated_at
		 FROM domains ORDER BY domain`,
	)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	var domains []*types.Domain
	for rows.Next() {
		d, err := scanDomainRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan domain row: %w", err)
		}
		domains = append(domains, &d)
	}
	return domains, rows.Err()
}

// ListDomainsByApp retrieves all domains for a specific app.
func ListDomainsByApp(db *sql.DB, appID string) ([]*types.Domain, error) {
	rows, err := db.Query(
		`SELECT id, app_id, domain, created_at, updated_at
		 FROM domains WHERE app_id = ? ORDER BY domain`, appID,
	)
	if err != nil {
		return nil, fmt.Errorf("list domains by app: %w", err)
	}
	defer rows.Close()

	var domains []*types.Domain
	for rows.Next() {
		d, err := scanDomainRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan domain row: %w", err)
		}
		domains = append(domains, &d)
	}
	return domains, rows.Err()
}

// DeleteDomain removes a domain by its ID.
func DeleteDomain(db *sql.DB, id string) error {
	_, err := db.Exec(`DELETE FROM domains WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	return nil
}

// DeleteDomainByDomain removes a domain by its domain name.
func DeleteDomainByDomain(db *sql.DB, domain string) error {
	_, err := db.Exec(`DELETE FROM domains WHERE domain = ?`, domain)
	if err != nil {
		return fmt.Errorf("delete domain by domain: %w", err)
	}
	return nil
}
