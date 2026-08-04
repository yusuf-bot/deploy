package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Filter describes criteria for selecting audit entries. Zero values mean no
// filter; a zero time.Time means no bound.
type Filter struct {
	App    string
	Action string
	By     string
	Since  time.Time
	Until  time.Time
	Limit  int
}

// readAll reads every entry from an audit log file in file order. A missing
// file yields an empty (non-nil) slice.
func readAll(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Entry{}, nil
		}
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	return entries, nil
}

// ReadRecent reads the most recent N entries from the audit log.
func ReadRecent(n int) ([]Entry, error) {
	entries, err := readAll(auditLogPath())
	if err != nil {
		return nil, err
	}

	// Sort by time descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Time.After(entries[j].Time)
	})

	if n <= 0 || n > len(entries) {
		n = len(entries)
	}
	return entries[:n], nil
}

// ReadFiltered reads audit entries matching the given filter, sorted by time
// descending, limited to f.Limit entries when f.Limit > 0.
func ReadFiltered(f Filter) ([]Entry, error) {
	return readFilteredFrom(auditLogPath(), f)
}

// readFilteredFrom filters entries from the audit file at path.
func readFilteredFrom(path string, f Filter) ([]Entry, error) {
	entries, err := readAll(path)
	if err != nil {
		return nil, err
	}

	filtered := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if f.App != "" && e.App != f.App {
			continue
		}
		if f.Action != "" && e.Action != f.Action {
			continue
		}
		if f.By != "" && e.InitiatedBy != f.By {
			continue
		}
		if !f.Since.IsZero() && e.Time.Before(f.Since) {
			continue
		}
		if !f.Until.IsZero() && e.Time.After(f.Until) {
			continue
		}
		filtered = append(filtered, e)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Time.After(filtered[j].Time)
	})

	if f.Limit > 0 && f.Limit < len(filtered) {
		filtered = filtered[:f.Limit]
	}
	return filtered, nil
}

// PruneOlderThan removes audit entries older than age and returns the number
// removed. Survivors are rewritten atomically to the same file.
func PruneOlderThan(age time.Duration) (int, error) {
	return pruneFrom(auditLogPath(), age)
}

// pruneFrom removes entries older than age from the audit file at path,
// rewriting survivors atomically. A missing file removes nothing.
func pruneFrom(path string, age time.Duration) (int, error) {
	entries, err := readAll(path)
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-age)
	survivors := make([]Entry, 0, len(entries))
	removed := 0
	for _, e := range entries {
		if e.Time.Before(cutoff) {
			removed++
			continue
		}
		survivors = append(survivors, e)
	}

	if removed == 0 {
		return 0, nil
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".audit-*.tmp")
	if err != nil {
		return 0, fmt.Errorf("create temp audit log: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("chmod temp audit log: %w", err)
	}

	enc := json.NewEncoder(tmp)
	for _, e := range survivors {
		if err := enc.Encode(e); err != nil {
			tmp.Close()
			return 0, fmt.Errorf("write temp audit log: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temp audit log: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return 0, fmt.Errorf("replace audit log: %w", err)
	}
	return removed, nil
}
