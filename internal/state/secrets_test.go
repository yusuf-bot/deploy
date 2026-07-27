package state

import (
	"os"
	"bytes"
	"testing"

	"deploy/internal/types"
)

// testKey is a deterministic 32-byte key for tests.
var testKey = bytes.Repeat([]byte{0x01}, 32)

func TestSetAndGetSecret(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "secret-app")

	s := &types.Secret{
		AppID: app.ID,
		Key:   "DB_PASSWORD",
		Value: "supersecret123",
	}
	created, err := SetSecret(db, s, testKey)
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if created.Key != "DB_PASSWORD" {
		t.Errorf("expected key DB_PASSWORD, got %s", created.Key)
	}

	got, err := GetSecret(db, app.ID, "DB_PASSWORD", testKey)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got == nil {
		t.Fatal("expected secret, got nil")
	}
	if got.Value != "supersecret123" {
		t.Errorf("expected value supersecret123, got %s", got.Value)
	}
}

func TestGetSecretNotFound(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "secret-notfound")

	got, err := GetSecret(db, app.ID, "NONEXISTENT", testKey)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent secret")
	}
}

func TestUpdateExistingSecret(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "secret-update")

	s := &types.Secret{AppID: app.ID, Key: "API_KEY", Value: "old-value"}
	SetSecret(db, s, testKey)

	s.Value = "new-value"
	updated, err := SetSecret(db, s, testKey)
	if err != nil {
		t.Fatalf("SetSecret update: %v", err)
	}
	if updated.Key != "API_KEY" {
		t.Errorf("expected key API_KEY, got %s", updated.Key)
	}

	got, err := GetSecret(db, app.ID, "API_KEY", testKey)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got.Value != "new-value" {
		t.Errorf("expected new-value, got %s", got.Value)
	}
}

func TestListSecrets(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "secret-list")

	secrets := []*types.Secret{
		{AppID: app.ID, Key: "KEY_A", Value: "val-a"},
		{AppID: app.ID, Key: "KEY_B", Value: "val-b"},
		{AppID: app.ID, Key: "KEY_C", Value: "val-c"},
	}
	for _, s := range secrets {
		SetSecret(db, s, testKey)
	}

	got, err := ListSecrets(db, app.ID, testKey)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 secrets, got %d", len(got))
	}
	// Check ordering by key
	if got[0].Key != "KEY_A" {
		t.Errorf("expected KEY_A first, got %s", got[0].Key)
	}
	if got[0].Value != "val-a" {
		t.Errorf("expected val-a, got %s", got[0].Value)
	}
}

func TestListSecretsByApp(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "secret-envmap")

	secrets := []*types.Secret{
		{AppID: app.ID, Key: "DB_URL", Value: "postgres://localhost/db"},
		{AppID: app.ID, Key: "API_KEY", Value: "abc123"},
	}
	for _, s := range secrets {
		SetSecret(db, s, testKey)
	}

	env, err := ListSecretsByApp(db, app.ID, testKey)
	if err != nil {
		t.Fatalf("ListSecretsByApp: %v", err)
	}
	if len(env) != 2 {
		t.Fatalf("expected 2 env entries, got %d", len(env))
	}
	if env["DB_URL"] != "postgres://localhost/db" {
		t.Errorf("expected postgres://localhost/db, got %s", env["DB_URL"])
	}
	if env["API_KEY"] != "abc123" {
		t.Errorf("expected abc123, got %s", env["API_KEY"])
	}
}

func TestDeleteSecret(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "secret-delete")

	s := &types.Secret{AppID: app.ID, Key: "TO_DELETE", Value: "delete-me"}
	SetSecret(db, s, testKey)

	if err := DeleteSecret(db, app.ID, "TO_DELETE"); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	got, err := GetSecret(db, app.ID, "TO_DELETE", testKey)
	if err != nil {
		t.Fatalf("GetSecret after delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestDeleteSecretNotFound(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "secret-del-notfound")

	err := DeleteSecret(db, app.ID, "NONEXISTENT")
	if err == nil {
		t.Fatal("expected error for deleting nonexistent secret")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	original := "my-sensitive-value"
	encrypted, err := encryptSecret(original, testKey)
	if err != nil {
		t.Fatalf("encryptSecret: %v", err)
	}
	if encrypted == original {
		t.Error("encrypted value should differ from plaintext")
	}

	decrypted, err := decryptSecret(encrypted, testKey)
	if err != nil {
		t.Fatalf("decryptSecret: %v", err)
	}
	if decrypted != original {
		t.Errorf("round-trip failed: got %q, want %q", decrypted, original)
	}
}

func TestEncryptWrongKey(t *testing.T) {
	original := "sensitive-data"
	encrypted, err := encryptSecret(original, testKey)
	if err != nil {
		t.Fatalf("encryptSecret: %v", err)
	}

	wrongKey := bytes.Repeat([]byte{0x02}, 32)
	_, err = decryptSecret(encrypted, wrongKey)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestEncryptDifferentCiphertexts(t *testing.T) {
	// Same plaintext with same key should produce different ciphertexts (random nonce)
	v1, err := encryptSecret("same-value", testKey)
	if err != nil {
		t.Fatalf("encryptSecret v1: %v", err)
	}
	v2, err := encryptSecret("same-value", testKey)
	if err != nil {
		t.Fatalf("encryptSecret v2: %v", err)
	}
	if v1 == v2 {
		t.Error("encrypted values should differ due to random nonce")
	}
}

func TestGenerateMasterKey(t *testing.T) {
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	if len(key) != KeySize {
		t.Errorf("expected key size %d, got %d", KeySize, len(key))
	}
}

func TestEnsureMasterKey(t *testing.T) {
	dir := t.TempDir()

	// First call should create the key
	key1, err := EnsureMasterKey(dir)
	if err != nil {
		t.Fatalf("EnsureMasterKey first call: %v", err)
	}
	if len(key1) != KeySize {
		t.Errorf("expected key size %d, got %d", KeySize, len(key1))
	}

	// Second call should load the same key
	key2, err := EnsureMasterKey(dir)
	if err != nil {
		t.Fatalf("EnsureMasterKey second call: %v", err)
	}
	if !bytes.Equal(key1, key2) {
		t.Error("master key should be stable across calls")
	}
}

func TestLoadMasterKey(t *testing.T) {
	dir := t.TempDir()

	// Generate and store a key
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	path := dir + "/" + MasterKeyFile
	if err := os.WriteFile(path, key, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded, err := LoadMasterKey(dir)
	if err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	if !bytes.Equal(key, loaded) {
		t.Error("loaded key should match stored key")
	}
}
