package crypto

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	crand "crypto/rand"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, keySize)
	for i := range key {
		key[i] = byte(i)
	}

	blob, err := Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	plain, err := Decrypt(key, blob)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if string(plain) != "secret" {
		t.Fatalf("Decrypt() = %q, want %q", plain, "secret")
	}
}

func TestDecryptRejectsShortBlob(t *testing.T) {
	key := make([]byte, keySize)
	if _, err := Decrypt(key, []byte{1, 2, 3}); err == nil {
		t.Fatal("Decrypt() error = nil, want error")
	}
	if _, err := Decrypt([]byte("short"), []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}); err == nil {
		t.Fatal("Decrypt() error = nil, want invalid key error")
	}
}

func TestGenerateKeySuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gen.key")
	key, err := GenerateKey(path)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	if len(key) != keySize {
		t.Fatalf("GenerateKey() len = %d, want %d", len(key), keySize)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("generated key file missing: %v", err)
	}

	badParent := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(badParent, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateKey(filepath.Join(badParent, "gen.key")); err == nil {
		t.Fatal("GenerateKey() error = nil, want write failure")
	}
}

func TestGenerateLoadAndLoadOrGenerateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "default.key")

	key, err := LoadOrGenerateKey(path)
	if err != nil {
		t.Fatalf("LoadOrGenerateKey() error = %v", err)
	}
	if got, want := len(key), keySize; got != want {
		t.Fatalf("generated key length = %d, want %d", got, want)
	}

	loaded, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey() error = %v", err)
	}
	if string(loaded) != string(key) {
		t.Fatal("LoadKey() did not return the generated key")
	}

	reused, err := LoadOrGenerateKey(path)
	if err != nil {
		t.Fatalf("LoadOrGenerateKey(existing) error = %v", err)
	}
	if string(reused) != string(key) {
		t.Fatal("LoadOrGenerateKey(existing) did not reuse the existing key")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestCryptoErrorPaths(t *testing.T) {
	origReader := crand.Reader
	crand.Reader = errReader{}
	if _, err := GenerateKey(filepath.Join(t.TempDir(), "bad.key")); err == nil {
		t.Fatal("GenerateKey() error = nil, want rand error")
	}
	if _, err := Encrypt(make([]byte, keySize), []byte("secret")); err == nil {
		t.Fatal("Encrypt() error = nil, want rand error")
	}
	crand.Reader = origReader

	if _, err := LoadKey(filepath.Join(t.TempDir(), "missing.key")); err == nil {
		t.Fatal("LoadKey() error = nil, want missing file error")
	}
	if _, err := Encrypt([]byte("short"), []byte("x")); err == nil {
		t.Fatal("Encrypt() error = nil, want key length error")
	}

	key := make([]byte, keySize)
	for i := range key {
		key[i] = byte(i)
	}
	blob, err := Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := make([]byte, keySize)
	wrongKey[0] = 99
	if _, err := Decrypt(wrongKey, blob); err == nil {
		t.Fatal("Decrypt() error = nil, want auth failure")
	}
	if _, err := Decrypt(key, []byte{1, 2, 3}); err == nil {
		t.Fatal("Decrypt() error = nil, want short blob error")
	}
}

func TestEncryptFileAndDecryptFileErrorPaths(t *testing.T) {
	key := make([]byte, keySize)
	for i := range key {
		key[i] = byte(255 - i)
	}
	root := t.TempDir()
	badParent := filepath.Join(root, "parent")
	if err := os.WriteFile(badParent, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptFile(key, filepath.Join(badParent, "secret.enc"), []byte("x")); err == nil {
		t.Fatal("EncryptFile() error = nil, want mkdir failure")
	}
	if _, err := DecryptFile(key, filepath.Join(root, "missing.enc")); err == nil {
		t.Fatal("DecryptFile() error = nil, want missing file error")
	}
}

func TestLoadKeyRejectsWrongLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.key")
	if err := os.WriteFile(path, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(path); err == nil {
		t.Fatal("LoadKey() error = nil, want length error")
	}
}

func TestEncryptFileDecryptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.enc")
	key := make([]byte, keySize)
	for i := range key {
		key[i] = byte(255 - i)
	}

	if err := EncryptFile(key, path, []byte("top secret")); err != nil {
		t.Fatalf("EncryptFile() error = %v", err)
	}
	plain, err := DecryptFile(key, path)
	if err != nil {
		t.Fatalf("DecryptFile() error = %v", err)
	}
	if string(plain) != "top secret" {
		t.Fatalf("DecryptFile() = %q, want %q", plain, "top secret")
	}

	if err := EncryptFile([]byte("short"), filepath.Join(t.TempDir(), "bad.enc"), []byte("x")); err == nil {
		t.Fatal("EncryptFile() error = nil, want invalid key error")
	}
	bad := filepath.Join(t.TempDir(), "badblob.enc")
	if err := os.WriteFile(bad, []byte("not-encrypted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptFile(key, bad); err == nil {
		t.Fatal("DecryptFile() error = nil, want decrypt error")
	}
}
