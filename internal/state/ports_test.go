package state

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func setupPortsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// TestAllocatePortSkipsPortsWhoseSuccessorIsTaken verifies the allocator
// reserves both the base port and the blue/green staging port (base+1):
// a base port whose successor is already allocated must be skipped.
func TestAllocatePortSkipsPortsWhoseSuccessorIsTaken(t *testing.T) {
	db := setupPortsTestDB(t)

	// Simulate profen owning final port 20001 (allocated from base 20000).
	// The next allocator run must NOT pick base 20000 (staging would hit 20001).
	if _, err := db.Exec("INSERT INTO port_allocations (app_name, port) VALUES (?, ?)", "profen", 20001); err != nil {
		t.Fatalf("seed profen: %v", err)
	}

	port, err := AllocatePort(db, "promist", nil)
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if port == 20000 {
		t.Fatalf("allocated base 20000 whose staging port 20001 is taken; got port %d", port)
	}

	// The chosen base must not collide with the existing allocation.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM port_allocations WHERE port IN (?, ?)", port, port+1).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 { // only the newly allocated base row should exist
		t.Errorf("expected only the new allocation, got count %d", count)
	}
}

// TestAllocatePortReusesExistingAllocation verifies existing apps keep their port.
func TestAllocatePortReusesExistingAllocation(t *testing.T) {
	db := setupPortsTestDB(t)
	if _, err := db.Exec("INSERT INTO port_allocations (app_name, port) VALUES (?, ?)", "existing", 20100); err != nil {
		t.Fatalf("seed: %v", err)
	}
	port, err := AllocatePort(db, "existing", nil)
	if err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if port != 20100 {
		t.Errorf("expected reuse of 20100, got %d", port)
	}
}
