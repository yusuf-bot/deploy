package state

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

const (
	// SettingBackupSchedule is the scheduled-backup cadence setting
	// ("daily HH:MM" or "weekly DOW HH:MM"; empty disables).
	SettingBackupSchedule = "backup_schedule"
	// SettingBackupRetention is the number of per-app backup archives kept per
	// app by the scheduled backup runner.
	SettingBackupRetention = "backup_retention"
	// DefaultBackupRetention is used when backup_retention is unset.
	DefaultBackupRetention = 3
)

// GetSetting retrieves a setting value by key.
func GetSetting(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return value, nil
}

// SetSetting sets a setting value by key (inserts or updates).
func SetSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?",
		key, value, value,
	)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// GetAllSettings returns all settings as a map.
func GetAllSettings(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query("SELECT key, value FROM settings ORDER BY key")
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		settings[k] = v
	}
	return settings, rows.Err()
}

// EncryptedSetSetting encrypts the value with master key before storing.
func EncryptedSetSetting(db *sql.DB, key, value string, masterKey []byte) error {
	encrypted, err := EncryptSecret([]byte(value), masterKey)
	if err != nil {
		return fmt.Errorf("encrypt setting %q: %w", key, err)
	}
	return SetSetting(db, key, "enc:"+encrypted)
}

// EncryptedGetSetting decrypts a value stored by EncryptedSetSetting.
// Returns the plaintext value. For backward compatibility, if the stored
// value does not have the "enc:" prefix it is returned as-is.
func EncryptedGetSetting(db *sql.DB, key string, masterKey []byte) (string, error) {
	val, err := GetSetting(db, key)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(val, "enc:") {
		return val, nil
	}
	decrypted, err := DecryptSecret(val[4:], masterKey)
	if err != nil {
		return "", fmt.Errorf("decrypt setting %q: %w", key, err)
	}
	return string(decrypted), nil
}

// GetBackupSchedule returns the parsed backup_schedule setting, or nil when
// unset/empty (scheduled backups off).
func GetBackupSchedule(db *sql.DB) (*BackupSchedule, error) {
	val, err := GetSetting(db, SettingBackupSchedule)
	if err != nil {
		return nil, err
	}
	return ParseBackupSchedule(val)
}

// GetBackupRetention returns the backup_retention setting, defaulting to
// DefaultBackupRetention when unset. A stored value below 1 is clamped to the
// default (settings are validated on set, so this only guards tampered data).
func GetBackupRetention(db *sql.DB) (int, error) {
	val, err := GetSetting(db, SettingBackupRetention)
	if err != nil {
		return 0, err
	}
	if val == "" {
		return DefaultBackupRetention, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 1 {
		return DefaultBackupRetention, nil
	}
	return n, nil
}
