package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestLog(t *testing.T, path string, entries []Entry) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("create test log: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatalf("write test entry: %v", err)
		}
	}
}

func testEntries() []Entry {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	return []Entry{
		{Time: base.Add(3 * time.Hour), Action: "deploy", App: "web", InitiatedBy: "alice", DurationMs: 120, Result: "success"},
		{Time: base.Add(2 * time.Hour), Action: "deploy", App: "api", InitiatedBy: "bob", DurationMs: 80, Result: "success"},
		{Time: base.Add(1 * time.Hour), Action: "stop", App: "web", InitiatedBy: "alice", DurationMs: 10, Result: "success"},
		{Time: base, Action: "start", App: "worker", InitiatedBy: "carol", DurationMs: 5, Result: "failed"},
	}
}

func TestReadFilteredFrom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	writeTestLog(t, path, testEntries())

	t.Run("filter by app", func(t *testing.T) {
		got, err := readFilteredFrom(path, Filter{App: "web"})
		if err != nil {
			t.Fatalf("readFilteredFrom: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d entries, want 2", len(got))
		}
		for _, e := range got {
			if e.App != "web" {
				t.Fatalf("entry app = %q, want web", e.App)
			}
		}
	})

	t.Run("filter by action", func(t *testing.T) {
		got, err := readFilteredFrom(path, Filter{Action: "deploy"})
		if err != nil {
			t.Fatalf("readFilteredFrom: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d entries, want 2", len(got))
		}
	})

	t.Run("filter by by", func(t *testing.T) {
		got, err := readFilteredFrom(path, Filter{By: "alice"})
		if err != nil {
			t.Fatalf("readFilteredFrom: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d entries, want 2", len(got))
		}
		for _, e := range got {
			if e.InitiatedBy != "alice" {
				t.Fatalf("entry by = %q, want alice", e.InitiatedBy)
			}
		}
	})

	t.Run("since bound", func(t *testing.T) {
		since := time.Date(2026, 8, 1, 13, 30, 0, 0, time.UTC)
		got, err := readFilteredFrom(path, Filter{Since: since})
		if err != nil {
			t.Fatalf("readFilteredFrom: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d entries, want 2", len(got))
		}
		for _, e := range got {
			if e.Time.Before(since) {
				t.Fatalf("entry time %v before since %v", e.Time, since)
			}
		}
	})

	t.Run("limit", func(t *testing.T) {
		got, err := readFilteredFrom(path, Filter{Limit: 2})
		if err != nil {
			t.Fatalf("readFilteredFrom: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d entries, want 2", len(got))
		}
		// Sorted by time desc, so first two are the newest.
		if got[0].Time.Before(got[1].Time) {
			t.Fatalf("entries not sorted by time desc")
		}
	})
}

func TestPruneFrom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	now := time.Now()
	entries := []Entry{
		{Time: now.Add(-2 * 24 * time.Hour), Action: "deploy", App: "web", InitiatedBy: "alice", Result: "success"},
		{Time: now.Add(-10 * 24 * time.Hour), Action: "deploy", App: "api", InitiatedBy: "bob", Result: "success"},
		{Time: now.Add(-3 * time.Hour), Action: "stop", App: "web", InitiatedBy: "alice", Result: "success"},
	}
	writeTestLog(t, path, entries)

	removed, err := pruneFrom(path, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("pruneFrom: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want 1", removed)
	}

	// Remaining entries are the two within 7 days.
	got, err := readFilteredFrom(path, Filter{})
	if err != nil {
		t.Fatalf("readFilteredFrom after prune: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries after prune, want 2", len(got))
	}
	for _, e := range got {
		if e.App == "api" {
			t.Fatalf("old api entry survived prune")
		}
	}

	// File is still valid JSONL with 0600 perms.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after prune: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("perms = %o, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after prune: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("file has %d lines, want 2", len(lines))
	}
	for _, line := range lines {
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("file no longer valid JSONL: %v", err)
		}
	}
}

func TestPruneFromMissingFile(t *testing.T) {
	removed, err := pruneFrom(filepath.Join(t.TempDir(), "missing.log"), 24*time.Hour)
	if err != nil {
		t.Fatalf("pruneFrom missing: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed %d, want 0", removed)
	}
}
