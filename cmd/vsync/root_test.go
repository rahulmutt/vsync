package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
)

func TestResolveHelpers(t *testing.T) {
	t.Setenv("VAULT_ADDR", "http://env-addr")
	t.Setenv("VAULT_TOKEN", "env-token")
	t.Setenv("VSYNC_CONFIG", "/tmp/config.yaml")

	flagVaultAddr = ""
	flagVaultToken = ""
	flagConfigPath = ""
	flagKeyPath = ""
	defer func() {
		flagVaultAddr = ""
		flagVaultToken = ""
		flagConfigPath = ""
		flagKeyPath = ""
	}()

	if got := resolveVaultAddr(); got != "http://env-addr" {
		t.Fatalf("resolveVaultAddr() = %q", got)
	}
	if got := resolveVaultToken(); got != "env-token" {
		t.Fatalf("resolveVaultToken() = %q", got)
	}
	if got, err := resolveConfigPath(); err != nil || got != "/tmp/config.yaml" {
		t.Fatalf("resolveConfigPath() = (%q, %v)", got, err)
	}
}

func TestResolveKeyUsesFlagVsEnvVsDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}

	keyPath := filepath.Join(t.TempDir(), "custom.key")
	key, err := crypto.GenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("generated key length = %d", len(key))
	}

	flagKeyPath = keyPath
	defer func() { flagKeyPath = "" }()
	loaded, err := resolveKey(dirs)
	if err != nil {
		t.Fatalf("resolveKey() error = %v", err)
	}
	if string(loaded) != string(key) {
		t.Fatal("resolveKey() did not load the key from flag path")
	}
}

func TestWriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "file.txt")
	if err := writeFile(path, "0640", []byte("hello")); err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want hello", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0640); got != want {
		t.Fatalf("perm = %v, want %v", got, want)
	}
}

func TestToSet(t *testing.T) {
	set := toSet([]string{"a", "b", "a"})
	if len(set) != 2 || !set["a"] || !set["b"] {
		t.Fatalf("toSet() = %#v", set)
	}
}
