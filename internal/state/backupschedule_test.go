package state

import (
	"testing"
	"time"
)

func TestParseBackupSchedule(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    *BackupSchedule
		wantErr bool
	}{
		{name: "empty disables", in: "", want: nil},
		{name: "blank disables", in: "   ", want: nil},

		{name: "daily midnight", in: "daily 00:00", want: &BackupSchedule{Hour: 0, Minute: 0}},
		{name: "daily boundary minute", in: "daily 23:59", want: &BackupSchedule{Hour: 23, Minute: 59}},
		{name: "daily padded hour", in: "daily 03:05", want: &BackupSchedule{Hour: 3, Minute: 5}},
		{name: "daily case-insensitive keyword", in: "Daily 03:00", want: &BackupSchedule{Hour: 3, Minute: 0}},
		{name: "daily trimmed", in: "  daily 03:00  ", want: &BackupSchedule{Hour: 3, Minute: 0}},
		{name: "daily two-digit", in: "daily 15:45", want: &BackupSchedule{Hour: 15, Minute: 45}},
		{name: "daily unpadded hour", in: "daily 3:00", want: &BackupSchedule{Hour: 3, Minute: 0}},
		{name: "daily unpadded minute", in: "daily 03:5", want: &BackupSchedule{Hour: 3, Minute: 5}},

		{name: "weekly mon", in: "weekly mon 02:30", want: &BackupSchedule{Weekly: true, Weekday: time.Monday, Hour: 2, Minute: 30}},
		{name: "weekly sun", in: "weekly sun 00:00", want: &BackupSchedule{Weekly: true, Weekday: time.Sunday, Hour: 0, Minute: 0}},
		{name: "weekly case-insensitive dow", in: "weekly SUN 12:00", want: &BackupSchedule{Weekly: true, Weekday: time.Sunday, Hour: 12, Minute: 0}},
		{name: "weekly sat", in: "weekly sat 23:59", want: &BackupSchedule{Weekly: true, Weekday: time.Saturday, Hour: 23, Minute: 59}},

		{name: "no keyword", in: "nightly 03:00", wantErr: true},
		{name: "daily no time", in: "daily", wantErr: true},
		{name: "daily too many fields", in: "daily 03:00 extra", wantErr: true},
		{name: "daily hour out of range", in: "daily 24:00", wantErr: true},
		{name: "daily hour negative", in: "daily -1:00", wantErr: true},
		{name: "daily minute out of range", in: "daily 03:60", wantErr: true},
		{name: "daily minute negative", in: "daily 03:-1", wantErr: true},
		{name: "daily non-numeric", in: "daily 0a:00", wantErr: true},
		{name: "daily missing colon", in: "daily 0300", wantErr: true},
		{name: "daily empty time", in: "daily :00", wantErr: true},
		{name: "weekly missing dow", in: "weekly 03:00", wantErr: true},
		{name: "weekly bad dow", in: "weekly friday 03:00", wantErr: true},
		{name: "weekly missing time", in: "weekly mon", wantErr: true},
		{name: "weekly too many fields", in: "weekly mon 03:00 extra", wantErr: true},
		{name: "weekly hour out of range", in: "weekly mon 24:00", wantErr: true},
		{name: "weekly minute out of range", in: "weekly mon 03:60", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBackupSchedule(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseBackupSchedule(%q) expected error, got %+v", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBackupSchedule(%q): %v", tt.in, err)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("ParseBackupSchedule(%q) = %+v, want nil", tt.in, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ParseBackupSchedule(%q) = nil, want %+v", tt.in, tt.want)
			}
			if got.Weekly != tt.want.Weekly || got.Weekday != tt.want.Weekday || got.Hour != tt.want.Hour || got.Minute != tt.want.Minute {
				t.Errorf("ParseBackupSchedule(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestBackupScheduleMatch(t *testing.T) {
	daily, err := ParseBackupSchedule("daily 03:00")
	if err != nil {
		t.Fatalf("parse daily: %v", err)
	}
	weekly, err := ParseBackupSchedule("weekly mon 03:00")
	if err != nil {
		t.Fatalf("parse weekly: %v", err)
	}

	// 2026-08-03 is a Monday; 2026-08-04 is a Tuesday.
	monday := time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)
	tuesday := time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC)

	// Daily: matches every weekday at 03:00.
	if !daily.Match(monday) {
		t.Error("daily should match Monday 03:00")
	}
	if !daily.Match(tuesday) {
		t.Error("daily should match Tuesday 03:00")
	}
	if daily.Match(tuesday.Add(time.Minute)) {
		t.Error("daily should not match 03:01")
	}
	if daily.Match(tuesday.Add(-time.Hour)) {
		t.Error("daily should not match 02:00")
	}

	// Weekly: only Monday 03:00.
	if !weekly.Match(monday) {
		t.Error("weekly mon should match Monday 03:00")
	}
	if weekly.Match(tuesday) {
		t.Error("weekly mon should not match Tuesday 03:00")
	}
	if weekly.Match(monday.Add(time.Minute)) {
		t.Error("weekly mon should not match Monday 03:01")
	}
	if weekly.Match(monday.Add(-time.Hour)) {
		t.Error("weekly mon should not match Monday 02:00")
	}

	// Nil schedule never matches.
	var nilSched *BackupSchedule
	if nilSched.Match(monday) {
		t.Error("nil schedule should never match")
	}
}

func TestGetBackupSettings(t *testing.T) {
	db := setupTestDB(t)

	// Defaults when unset.
	sched, err := GetBackupSchedule(db)
	if err != nil {
		t.Fatalf("GetBackupSchedule default: %v", err)
	}
	if sched != nil {
		t.Fatalf("expected nil schedule by default, got %+v", sched)
	}
	ret, err := GetBackupRetention(db)
	if err != nil {
		t.Fatalf("GetBackupRetention default: %v", err)
	}
	if ret != DefaultBackupRetention {
		t.Errorf("expected default retention %d, got %d", DefaultBackupRetention, ret)
	}

	// Set both, read back.
	if err := SetSetting(db, SettingBackupSchedule, "weekly wed 04:30"); err != nil {
		t.Fatalf("set schedule: %v", err)
	}
	if err := SetSetting(db, SettingBackupRetention, "7"); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	sched, err = GetBackupSchedule(db)
	if err != nil {
		t.Fatalf("GetBackupSchedule: %v", err)
	}
	if sched == nil || !sched.Weekly || sched.Weekday != time.Wednesday || sched.Hour != 4 || sched.Minute != 30 {
		t.Errorf("unexpected schedule after set: %+v", sched)
	}
	ret, err = GetBackupRetention(db)
	if err != nil {
		t.Fatalf("GetBackupRetention: %v", err)
	}
	if ret != 7 {
		t.Errorf("expected retention 7, got %d", ret)
	}

	// Tampered/invalid stored retention clamps to default.
	if err := SetSetting(db, SettingBackupRetention, "0"); err != nil {
		t.Fatalf("set invalid retention: %v", err)
	}
	ret, err = GetBackupRetention(db)
	if err != nil {
		t.Fatalf("GetBackupRetention invalid: %v", err)
	}
	if ret != DefaultBackupRetention {
		t.Errorf("expected clamp to default %d, got %d", DefaultBackupRetention, ret)
	}
}
