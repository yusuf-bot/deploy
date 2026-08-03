//go:build integration

package integration

import (
	"fmt"
	"testing"
)

// TestDevContainerLifecycle asserts DevStart creates a separate dev container
// on a distinct port using the locally-built image, and DevStop removes it
// while leaving the main deployment running.
func TestDevContainerLifecycle(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()
	dir := h.writeFixture(t, name, "A")

	h.promote(t, name, dir)
	app := h.mustGetApp(t, name)
	port := app.Port
	devPort := port + 1000

	startResp, err := h.deployClient.DevStart(name)
	if err != nil {
		t.Fatalf("dev start: %v", err)
	}
	if startResp.Message == "" {
		t.Fatal("dev start returned no message")
	}

	devID := h.containerWithLabels(t, "deploy.managed=true", "deploy.dev=true", fmt.Sprintf("deploy.app.name=%s", name))
	if devID == "" {
		t.Fatal("no dev container found after DevStart")
	}
	if devID == app.ContainerID {
		t.Fatal("dev container ID should differ from the main container")
	}
	if !h.containerBindsPort(t, devID, devPort) {
		t.Fatalf("dev container does not bind distinct port %d", devPort)
	}
	// Main deployment unaffected and still healthy.
	h.assertHealth(t, port, "A")

	if _, err := h.deployClient.DevStop(name); err != nil {
		t.Fatalf("dev stop: %v", err)
	}
	if got := h.containerWithLabels(t, "deploy.managed=true", "deploy.dev=true", fmt.Sprintf("deploy.app.name=%s", name)); got != "" {
		t.Fatalf("dev container still present after DevStop: %s", got)
	}
	h.assertHealth(t, port, "A")
}
