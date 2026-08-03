//go:build integration

package integration

import (
	"testing"

	"deploy/internal/types"
)

// TestStartLocalOnlyImage asserts an app whose image exists only locally (it
// was built and never pushed to any registry) can be stopped and started
// without a registry pull (fix 15c7e6e). If the daemon attempted to pull
// <app>:<version> it would fail — that tag exists nowhere but the local daemon.
func TestStartLocalOnlyImage(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()
	dir := h.writeFixture(t, name, "A")

	h.promote(t, name, dir)
	app := h.mustGetApp(t, name)
	port := app.Port

	if _, err := h.deployClient.StopApp(name, false); err != nil {
		t.Fatalf("stop: %v", err)
	}
	app = h.mustGetApp(t, name)
	if app.Status != types.StatusStopped {
		t.Fatalf("status after stop: got %q, want %q", app.Status, types.StatusStopped)
	}
	if app.ContainerID != "" {
		t.Fatalf("container not removed on stop: %s", app.ContainerID)
	}

	if _, err := h.deployClient.StartApp(name, false); err != nil {
		t.Fatalf("start local-only image: %v", err)
	}
	app = h.mustGetApp(t, name)
	if app.Status != types.StatusRunning {
		t.Fatalf("status after start: got %q, want %q", app.Status, types.StatusRunning)
	}
	if app.ContainerID == "" {
		t.Fatal("no container after start")
	}
	h.assertHealth(t, port, "A")
}

// TestRestartLocalOnlyImage asserts the full stop+start cycle keeps the app on
// its stable port and healthy, again proving no registry pull is needed.
func TestRestartLocalOnlyImage(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()
	dir := h.writeFixture(t, name, "A")

	h.promote(t, name, dir)
	app := h.mustGetApp(t, name)
	port := app.Port

	if _, err := h.deployClient.StopApp(name, false); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := h.deployClient.StartApp(name, false); err != nil {
		t.Fatalf("start after stop: %v", err)
	}

	app = h.mustGetApp(t, name)
	if app.Status != types.StatusRunning {
		t.Fatalf("status after restart: got %q, want %q", app.Status, types.StatusRunning)
	}
	if app.Port != port {
		t.Fatalf("port drifted after restart: got %d, want %d", app.Port, port)
	}
	h.assertHealth(t, port, "A")
}
