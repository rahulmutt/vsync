package crypto

import (
	"os"
	"path/filepath"
	"testing"
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
}
