package state

import (
	"database/sql"
	"fmt"
	"time"

	"deploy/internal/types"
)

// encryptSecret encrypts a plaintext secret for storage using AES-256-GCM.
func encryptSecret(plaintext string, key []byte) (string, error) {
	return EncryptSecret([]byte(plaintext), key)
}

// decryptSecret decrypts a stored secret value using AES-256-GCM.
func decryptSecret(encoded string, key []byte) (string, error) {
	b, err := DecryptSecret(encoded, key)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(b), nil
}

// SetSecret creates or updates a secret for an app.
func SetSecret(db *sql.DB, s *types.Secret, key []byte) (*types.Secret, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	encrypted, err := encryptSecret(s.Value, key)
	if err != nil {
		return nil, fmt.Errorf("set secret: %w", err)
	}

	_, err = db.Exec(
		`INSERT INTO secrets (app_id, key, value, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(app_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		s.AppID, s.Key, encrypted, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("set secret: %w", err)
	}

	s.CreatedAt, _ = time.Parse(time.RFC3339, now)
	s.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	return s, nil
}

// GetSecret retrieves and decrypts a secret value.
func GetSecret(db *sql.DB, appID string, secretKey string, encKey []byte) (*types.Secret, error) {
	row := db.QueryRow(
		`SELECT app_id, key, value, created_at, updated_at
		 FROM secrets WHERE app_id = ? AND key = ?`, appID, secretKey,
	)

	var aID, k, encrypted, createdAt, updatedAt string
	err := row.Scan(&aID, &k, &encrypted, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get secret: %w", err)
	}

	plaintext, err := decryptSecret(encrypted, encKey)
	if err != nil {
		return nil, fmt.Errorf("get secret: %w", err)
	}

	secret := &types.Secret{
		AppID: aID,
		Key:   k,
		Value: plaintext,
	}
	secret.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	secret.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return secret, nil
}

// ListSecrets returns all secrets for an app with decrypted values.
func ListSecrets(db *sql.DB, appID string, encKey []byte) ([]types.Secret, error) {
	rows, err := db.Query(
		`SELECT app_id, key, value, created_at, updated_at
		 FROM secrets WHERE app_id = ? ORDER BY key`, appID,
	)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	defer rows.Close()

	var secrets []types.Secret
	for rows.Next() {
		var aID, k, encrypted, createdAt, updatedAt string
		if err := rows.Scan(&aID, &k, &encrypted, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan secret row: %w", err)
		}
		plaintext, decErr := decryptSecret(encrypted, encKey)
		if decErr != nil {
			return nil, fmt.Errorf("list secrets: %w", decErr)
		}
		s := types.Secret{AppID: aID, Key: k, Value: plaintext}
		s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		secrets = append(secrets, s)
	}
	return secrets, rows.Err()
}

// ListSecretsByApp returns a key-value map of all secrets for an app,
// decrypted and ready for environment injection.
func ListSecretsByApp(db *sql.DB, appID string, encKey []byte) (map[string]string, error) {
	rows, err := db.Query(
		`SELECT key, value FROM secrets WHERE app_id = ? ORDER BY key`, appID,
	)
	if err != nil {
		return nil, fmt.Errorf("list secrets by app: %w", err)
	}
	defer rows.Close()

	env := make(map[string]string)
	for rows.Next() {
		var k, encrypted string
		if err := rows.Scan(&k, &encrypted); err != nil {
			return nil, fmt.Errorf("scan secret row: %w", err)
		}
		plaintext, decErr := decryptSecret(encrypted, encKey)
		if decErr != nil {
			return nil, fmt.Errorf("list secrets by app: %w", decErr)
		}
		env[k] = plaintext
	}
	return env, rows.Err()
}

// DeleteSecret removes a single secret.
func DeleteSecret(db *sql.DB, appID string, key string) error {
	res, err := db.Exec(
		`DELETE FROM secrets WHERE app_id = ? AND key = ?`, appID, key,
	)
	if err != nil {
		return fmt.Errorf("delete secret: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("secret %q not found for app %q", key, appID)
	}
	return nil
}
