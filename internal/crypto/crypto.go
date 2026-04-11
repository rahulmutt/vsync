// Package crypto provides AES-256-GCM encryption/decryption for vsync.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/vsync/vsync/internal/state"
)

const (
	keySize        = 32 // AES-256
	nonceSize      = 12 // GCM standard nonce
	additionalData = "vsync/v1"
)

// GenerateKey creates a new 32-byte random key and writes it to path with mode 0600.
func GenerateKey(path string) ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	if err := state.WriteAtomic(path, key, 0600); err != nil {
		return nil, fmt.Errorf("write key: %w", err)
	}
	return key, nil
}

// LoadKey reads an existing key from path.
func LoadKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", path, err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("invalid key length %d (want %d)", len(key), keySize)
	}
	return key, nil
}

// LoadOrGenerateKey loads the key at path; if it doesn't exist, generates and saves it.
func LoadOrGenerateKey(path string) ([]byte, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return GenerateKey(path)
	}
	return LoadKey(path)
}

// Encrypt encrypts plaintext with AES-256-GCM using key.
// Output format: nonce (12 bytes) || ciphertext+tag.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(additionalData))
	return append(nonce, ciphertext...), nil
}

// Decrypt decrypts a blob produced by Encrypt.
func Decrypt(key, blob []byte) ([]byte, error) {
	if len(blob) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, ciphertext := blob[:nonceSize], blob[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, []byte(additionalData))
}

// EncryptFile encrypts plaintext and writes it to path with mode 0600.
func EncryptFile(key []byte, path string, plaintext []byte) error {
	blob, err := Encrypt(key, plaintext)
	if err != nil {
		return err
	}
	return state.WriteAtomic(path, blob, 0600)
}

// DecryptFile reads and decrypts a file written by EncryptFile.
func DecryptFile(key []byte, path string) ([]byte, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	plain, err := Decrypt(key, blob)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", path, err)
	}
	return plain, nil
}
