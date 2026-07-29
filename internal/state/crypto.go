package state

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const (
	// MasterKeyFile is the name of the master key file within the deploy directory.
	MasterKeyFile = "master.key"
	// KeySize is the size of the AES-256 key in bytes.
	KeySize = 32
)

// EnsureMasterKey generates a 256-bit key if one doesn't exist,
// stores it in keyDir/master.key with 0600 permissions.
// Returns the key bytes.
func EnsureMasterKey(keyDir string) ([]byte, error) {
	path := filepath.Join(keyDir, MasterKeyFile)
	if data, err := os.ReadFile(path); err == nil && len(data) == KeySize {
		return data, nil
	}
	log.Printf("warning: master key file has wrong size, generating new key")
	key, err := GenerateMasterKey()
	if err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	if err := os.WriteFile(path, key, 0600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}

// LoadMasterKey reads the key from the file in keyDir.
func LoadMasterKey(keyDir string) ([]byte, error) {
	path := filepath.Join(keyDir, MasterKeyFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load master key: %w", err)
	}
	if len(data) != KeySize {
		return nil, fmt.Errorf("invalid master key size: got %d, want %d", len(data), KeySize)
	}
	return data, nil
}

// GenerateMasterKey creates a random 256-bit key using crypto/rand.
func GenerateMasterKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return key, nil
}

// EncryptSecret encrypts plaintext using AES-256-GCM with a random nonce.
// Returns base64(nonce || ciphertext) where ciphertext includes the GCM auth tag.
func EncryptSecret(plaintext []byte, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	out := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// DecryptSecret decrypts the output of EncryptSecret.
// Input: base64(nonce || ciphertext).
func DecryptSecret(encoded string, key []byte) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}