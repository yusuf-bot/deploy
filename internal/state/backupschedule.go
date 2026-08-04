package state

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// BackupSchedule describes when automatic per-app backups run. It parses the
// backup_schedule setting value.
type BackupSchedule struct {
	Weekly  bool
	Weekday time.Weekday
	Hour    int
	Minute  int
}

// scheduleWeekdays maps the 3-letter weekday names used by the weekly format
// to time.Weekday values (Sunday is 0).
var scheduleWeekdays = map[string]time.Weekday{
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
	"sun": time.Sunday,
}

// ParseBackupSchedule parses a backup_schedule setting value. Accepted formats
// (case-insensitive, surrounding whitespace trimmed):
//
//	daily HH:MM         — every day at HH:MM (24-hour clock)
//	weekly DOW HH:MM    — every week on DOW (mon..sun) at HH:MM
//
// An empty string parses to nil, meaning scheduled backups are off. Anything
// else is an error.
func ParseBackupSchedule(s string) (*BackupSchedule, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return nil, nil
	}

	fields := strings.Fields(s)
	var sched BackupSchedule
	timeIdx := 1
	switch fields[0] {
	case "daily":
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid backup schedule %q: want \"daily HH:MM\"", s)
		}
	case "weekly":
		sched.Weekly = true
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid backup schedule %q: want \"weekly DOW HH:MM\" (DOW = mon..sun)", s)
		}
		dow, ok := scheduleWeekdays[fields[1]]
		if !ok {
			return nil, fmt.Errorf("invalid backup schedule %q: unknown weekday %q (use mon..sun)", s, fields[1])
		}
		sched.Weekday = dow
		timeIdx = 2
	default:
		return nil, fmt.Errorf("invalid backup schedule %q: want \"daily HH:MM\" or \"weekly DOW HH:MM\"", s)
	}

	hour, minute, err := parseScheduleTime(fields[timeIdx])
	if err != nil {
		return nil, fmt.Errorf("invalid backup schedule %q: %v", s, err)
	}
	sched.Hour = hour
	sched.Minute = minute
	return &sched, nil
}

// parseScheduleTime parses and bounds-checks an HH:MM (24-hour) time string.
func parseScheduleTime(s string) (hour, minute int, err error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, fmt.Errorf("want HH:MM, got %q", s)
	}
	hour, err = strconv.Atoi(h)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hour %q", h)
	}
	minute, err = strconv.Atoi(m)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minute %q", m)
	}
	if hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("hour must be 0-23, got %d", hour)
	}
	if minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("minute must be 0-59, got %d", minute)
	}
	return hour, minute, nil
}

// Match reports whether a backup should run at time t per this schedule.
// Daily schedules match on HH:MM alone; weekly schedules also require the
// weekday to match.
func (s *BackupSchedule) Match(t time.Time) bool {
	if s == nil {
		return false
	}
	if s.Hour != t.Hour() || s.Minute != t.Minute() {
		return false
	}
	if s.Weekly && s.Weekday != t.Weekday() {
		return false
	}
	return true
}
