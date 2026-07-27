package state

import (
	"database/sql"
	"fmt"
	"strings"
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
