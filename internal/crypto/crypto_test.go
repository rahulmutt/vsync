package crypto_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/vsync/vsync/internal/crypto"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "test.key")

	key, err := crypto.GenerateKey(keyPath)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(key))
	}

	plaintext := []byte("hello, vsync!")
	blob, err := crypto.Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got, err := crypto.Decrypt(key, blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("expected %q, got %q", plaintext, got)
	}
}

func TestEncryptFileDecryptFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "test.key")
	encPath := filepath.Join(dir, "secret.enc")

	key, _ := crypto.GenerateKey(keyPath)
	plaintext := []byte("super-secret-value-123")

	if err := crypto.EncryptFile(key, encPath, plaintext); err != nil {
		t.Fatalf("EncryptFile: %v", err)
	}

	// File must be mode 0600.
	info, _ := os.Stat(encPath)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected mode 0600, got %v", info.Mode().Perm())
	}

	got, err := crypto.DecryptFile(key, encPath)
	if err != nil {
		t.Fatalf("DecryptFile: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("expected %q, got %q", plaintext, got)
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	dir := t.TempDir()
	key1, _ := crypto.GenerateKey(filepath.Join(dir, "key1.key"))
	key2, _ := crypto.GenerateKey(filepath.Join(dir, "key2.key"))

	blob, _ := crypto.Encrypt(key1, []byte("secret"))
	_, err := crypto.Decrypt(key2, blob)
	if err == nil {
		t.Fatal("expected decryption with wrong key to fail")
	}
}

func TestLoadOrGenerateKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "new.key")

	// First call: generates.
	key1, err := crypto.LoadOrGenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	// Second call: loads the same key.
	key2, err := crypto.LoadOrGenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(key1, key2) {
		t.Fatal("loaded key differs from generated key")
	}
}
