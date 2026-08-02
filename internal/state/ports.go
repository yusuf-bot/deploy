package state

import (
	"database/sql"
	"fmt"
)

const portRangeStart = 20000
const portRangeEnd = 30000

// AllocatePort assigns a unique port to an app from the pool.
// Checks both the DB table and Docker for actual usage.
func AllocatePort(db *sql.DB, appName string, checkPortInUse func(int) (bool, error)) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Check if app already has a port
	var existingPort int
	err = tx.QueryRow("SELECT port FROM port_allocations WHERE app_name = ?", appName).Scan(&existingPort)
	if err == nil {
		return existingPort, nil // App already has a port, reuse it
	}

	// Find first available port. The promote flow stages new containers on
	// port+1 (blue/green), so both the base port AND its successor must be
	// free — otherwise a just-allocated base would collide with the app that
	// already owns port+1 (e.g. an app that was promoted to final port P+1).
	for port := portRangeStart; port < portRangeEnd; port++ {
		// Check DB
		var count int
		tx.QueryRow("SELECT COUNT(*) FROM port_allocations WHERE port IN (?, ?)", port, port+1).Scan(&count)
		if count > 0 {
			continue
		}
		// Check Docker
		if checkPortInUse != nil {
			inUse, err := checkPortInUse(port)
			if err != nil || inUse {
				continue
			}
			inUse, err = checkPortInUse(port + 1)
			if err != nil || inUse {
				continue
			}
		}
		// Allocate
		_, err := tx.Exec("INSERT INTO port_allocations (app_name, port) VALUES (?, ?)", appName, port)
		if err != nil {
			continue // Race condition, try next port
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit port allocation: %w", err)
		}
		return port, nil
	}
	return 0, fmt.Errorf("no available ports in range %d-%d", portRangeStart, portRangeEnd)
}

// ReleasePort frees a port back to the pool.
func ReleasePort(db *sql.DB, appName string) error {
	_, err := db.Exec("DELETE FROM port_allocations WHERE app_name = ?", appName)
	return err
}

// GetPort returns the allocated port for an app.
func GetPort(db *sql.DB, appName string) (int, error) {
	var port int
	err := db.QueryRow("SELECT port FROM port_allocations WHERE app_name = ?", appName).Scan(&port)
	if err != nil {
		return 0, fmt.Errorf("get port for %s: %w", appName, err)
	}
	return port, nil
}

// UpdatePortAllocation atomically replaces the port assigned to an app.
// Used after promote to update the stable port for the app.
func UpdatePortAllocation(db *sql.DB, appName string, newPort int) error {
	_, err := db.Exec(
		"INSERT OR REPLACE INTO port_allocations (app_name, port) VALUES (?, ?)",
		appName, newPort,
	)
	if err != nil {
		return fmt.Errorf("update port allocation for %s: %w", appName, err)
	}
	return nil
}
