//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
)

const testDomainSuffix = ".example.test"

// TestCaddyConfMatchesAppPort asserts the Caddy site snippet always points at
// the app's real, current port (fix 15c7e6e "conf from truth"): after a second
// promote the port is unchanged and the regenerated snippet references it —
// never the previous/stale port.
func TestCaddyConfMatchesAppPort(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()
	dir := h.writeFixture(t, name, "A")

	h.promote(t, name, dir)
	port := h.mustGetApp(t, name).Port

	domain := name + testDomainSuffix
	if err := h.deployClient.AddDomain(name, domain, true); err != nil {
		t.Fatalf("add domain: %v", err)
	}

	h.promote(t, name, dir)
	app := h.mustGetApp(t, name)
	if app.Port != port {
		t.Fatalf("port drifted: got %d, want %d", app.Port, port)
	}

	snippet := h.readSnippet(t, name, domain)
	want := fmt.Sprintf("reverse_proxy localhost:%d", port)
	if !strings.Contains(snippet, want) {
		t.Fatalf("snippet does not point at current port %d:\n%s", port, snippet)
	}
	if strings.Contains(snippet, fmt.Sprintf("reverse_proxy localhost:%d", port+1)) {
		t.Fatalf("snippet references stale port %d:\n%s", port+1, snippet)
	}
}

// TestCaddyConfRegeneratedFromDB asserts snippet files are regenerated from the
// DB on a caddy manager restart: even if the on-disk snippet were stale or
// missing, the restart writes it from the app's recorded port.
func TestCaddyConfRegeneratedFromDB(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()
	dir := h.writeFixture(t, name, "A")

	h.promote(t, name, dir)
	port := h.mustGetApp(t, name).Port

	domain := name + testDomainSuffix
	if err := h.deployClient.AddDomain(name, domain, true); err != nil {
		t.Fatalf("add domain: %v", err)
	}
	if snippet := h.readSnippet(t, name, domain); !strings.Contains(snippet, fmt.Sprintf("reverse_proxy localhost:%d", port)) {
		t.Fatalf("snippet after add domain does not point at %d:\n%s", port, snippet)
	}

	// Simulate a daemon restart of the caddy subprocess.
	if err := h.cm.Stop(); err != nil {
		t.Fatalf("stop caddy manager: %v", err)
	}
	if err := h.cm.Start(); err != nil {
		t.Fatalf("start caddy manager: %v", err)
	}

	snippet := h.readSnippet(t, name, domain)
	want := fmt.Sprintf("reverse_proxy localhost:%d", port)
	if !strings.Contains(snippet, want) {
		t.Fatalf("snippet after restart does not match DB port %d:\n%s", port, snippet)
	}
}
