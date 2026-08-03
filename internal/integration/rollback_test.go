//go:build integration

package integration

import (
	"strings"
	"testing"
)

// TestRollbackToPrevious asserts rollback to the previous active deployment:
// the app moves back to the prior version (health marker A) on the same stable
// port.
//
// Note: the rollback handler loads deploy.yml from the daemon's working
// directory ("."), so the test chdirs to the v1 fixture before rolling back.
func TestRollbackToPrevious(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()

	dirA := h.writeFixture(t, name, "A")
	h.promote(t, name, dirA)
	port := h.mustGetApp(t, name).Port

	dirB := h.writeFixture(t, name, "B")
	h.promote(t, name, dirB)
	h.assertHealth(t, port, "B")

	t.Chdir(dirA) // rollback handler resolves deploy.yml from CWD
	resp, err := h.deployClient.Rollback(name, "")
	if err != nil {
		t.Fatalf("rollback to previous: %v", err)
	}
	if resp.Version == "" {
		t.Fatal("rollback response missing version")
	}

	app := h.mustGetApp(t, name)
	if app.Port != port {
		t.Fatalf("port drifted across rollback: got %d, want %d", app.Port, port)
	}
	h.assertHealth(t, port, "A")
}

// TestRollbackNoPrevious asserts rolling back with no prior deployment fails
// with a clear error instead of silently succeeding.
func TestRollbackNoPrevious(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()
	dir := h.writeFixture(t, name, "A")
	h.promote(t, name, dir)

	t.Chdir(dir)
	_, err := h.deployClient.Rollback(name, "")
	if err == nil {
		t.Fatal("expected rollback with no previous deployment to fail")
	}
	if !strings.Contains(err.Error(), "no previous deployment") {
		t.Fatalf("unexpected rollback error: %v", err)
	}
}
