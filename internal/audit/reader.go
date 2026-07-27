package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// ReadRecent reads the most recent N entries from the audit log.
func ReadRecent(n int) ([]Entry, error) {
	f, err := os.Open(auditLogPath())
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

	// Sort by time descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Time.After(entries[j].Time)
	})

	if n <= 0 || n > len(entries) {
		n = len(entries)
	}
	return entries[:n], nil
}
