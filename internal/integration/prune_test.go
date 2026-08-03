//go:build integration

package integration

import (
	"testing"
)

// TestPruneKeepsN asserts pruning keeps the newest N tarballs, never deletes
// the active deployment's tarball, and a dry run deletes nothing.
func TestPruneKeepsN(t *testing.T) {
	h := newHarness(t)
	name := h.newAppName()
	dir := h.writeFixture(t, name, "A")

	// Four promotes -> four saved tarballs (CleanOldTarballs keeps 5, so none
	// are auto-cleaned).
	for i := 1; i <= 4; i++ {
		h.promote(t, name, dir)
	}

	images, err := h.deployClient.ListImages(name)
	if err != nil {
		t.Fatalf("list images: %v", err)
	}
	if len(images) != 4 {
		t.Fatalf("expected 4 tarballs, got %d: %v", len(images), images)
	}

	status, err := h.deployClient.Status(name)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	active := status.ActiveDeployment.Version
	if active == "" {
		t.Fatal("no active deployment version")
	}

	// Dry run: reports what would be removed, deletes nothing.
	dryResp, err := h.deployClient.Prune(name, 2, true)
	if err != nil {
		t.Fatalf("dry-run prune: %v", err)
	}
	if !dryResp.DryRun {
		t.Fatal("dry run response not marked as dry run")
	}
	afterDry, err := h.deployClient.ListImages(name)
	if err != nil {
		t.Fatalf("list images after dry run: %v", err)
	}
	if len(afterDry) != 4 {
		t.Fatalf("dry run deleted tarballs: got %d, want 4", len(afterDry))
	}

	// Real prune: keeps exactly the 2 newest, active version still present.
	resp, err := h.deployClient.Prune(name, 2, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(resp.Apps) != 1 {
		t.Fatalf("expected prune result for one app, got %d", len(resp.Apps))
	}
	appResult := resp.Apps[0]
	if len(appResult.Removed) != 2 {
		t.Fatalf("expected 2 removed, got %d", len(appResult.Removed))
	}
	// PruneTarballs reports the active version as Protected (never deleted),
	// so kept+protected is the number of tarballs left on disk.
	if len(appResult.Kept)+len(appResult.Protected) != 2 {
		t.Fatalf("expected 2 tarballs kept (kept=%d protected=%d)", len(appResult.Kept), len(appResult.Protected))
	}

	after, err := h.deployClient.ListImages(name)
	if err != nil {
		t.Fatalf("list images after prune: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("expected 2 tarballs after prune, got %d: %v", len(after), after)
	}
	found := false
	for _, v := range after {
		if v == active {
			found = true
		}
	}
	if !found {
		t.Fatalf("active version %s was pruned; remaining: %v", active, after)
	}
}
