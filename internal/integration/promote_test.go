//go:build integration

package integration

import (
	"fmt"
	"testing"
)

// TestPromoteReusesPort asserts the deployment port is stable across promote
// cycles (fix 15c7e6e): consecutive promotes keep the app on the same host
// port, the new container replaces the old one, and the health endpoint keeps
// serving on the original port.
func TestPromoteReusesPort(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()
	dir := h.writeFixture(t, name, "A")

	resp1 := h.promote(t, name, dir)
	port1 := h.mustGetApp(t, name).Port

	resp2 := h.promote(t, name, dir)
	app := h.mustGetApp(t, name)

	if app.Port != port1 {
		t.Fatalf("port drifted across promotes: got %d, want %d", app.Port, port1)
	}
	if resp2.NewContainerID == resp1.NewContainerID {
		t.Fatalf("second promote reused the same container %s", resp2.NewContainerID)
	}
	if app.ContainerID == "" {
		t.Fatal("app has no container after promote")
	}
	// The new container must bind the app's stable port and stay healthy.
	h.assertHealth(t, port1, "A")
}

// TestPromoteHealthGatesSwap asserts a failed health check on a new container
// never cuts over: the app record still points at the old container, the port
// is unchanged, and the old container keeps serving traffic.
func TestPromoteHealthGatesSwap(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()
	goodDir := h.writeFixture(t, name, "GOOD")

	h.promote(t, name, goodDir)
	app := h.mustGetApp(t, name)
	oldContainerID := app.ContainerID
	port := app.Port
	if oldContainerID == "" {
		t.Fatal("expected a container after first promote")
	}

	// Second promote builds fine but never serves /health -> must fail.
	brokenDir := h.writeFixtureWithDockerfile(t, name, "BROKEN", nginxBrokenDockerfile)
	if err := h.promoteErr(t, name, brokenDir); err == nil {
		t.Fatal("expected promote of broken image to fail health check")
	}

	app = h.mustGetApp(t, name)
	if app.ContainerID != oldContainerID {
		t.Fatalf("app switched containers despite failed health check: got %s, want %s", app.ContainerID, oldContainerID)
	}
	if app.Port != port {
		t.Fatalf("port changed despite failed promote: got %d, want %d", app.Port, port)
	}
	// The old container must have been restarted and still serve traffic.
	h.assertHealth(t, port, "GOOD")
	if got := h.containerWithLabels(t, "deploy.managed=true", fmt.Sprintf("deploy.app.name=%s", name)); got == "" {
		t.Fatal("expected the old container to still exist")
	}
}
