package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
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

func TestResolveKeyUsesEnvAndDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}

	envKeyPath := filepath.Join(t.TempDir(), "env.key")
	envKey, err := crypto.GenerateKey(envKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	defaultKey, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}

	flagKeyPath = ""
	defer func() { flagKeyPath = "" }()

	t.Setenv("VSYNC_KEY", envKeyPath)
	loaded, err := resolveKey(dirs)
	if err != nil {
		t.Fatalf("resolveKey() with VSYNC_KEY error = %v", err)
	}
	if string(loaded) != string(envKey) {
		t.Fatalf("resolveKey() with VSYNC_KEY loaded %q, want env key", loaded)
	}

	t.Setenv("VSYNC_KEY", "")
	loaded, err = resolveKey(dirs)
	if err != nil {
		t.Fatalf("resolveKey() with default path error = %v", err)
	}
	if string(loaded) != string(defaultKey) {
		t.Fatalf("resolveKey() with default path loaded %q, want default key", loaded)
	}
}

func TestResolveConfigPathUsesFlagEnvAndDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	flagConfigPath = filepath.Join(home, "flag.yaml")
	defer func() { flagConfigPath = "" }()
	if got, err := resolveConfigPath(); err != nil || got != flagConfigPath {
		t.Fatalf("resolveConfigPath() with flag = (%q, %v), want flag path", got, err)
	}
	flagConfigPath = ""
	t.Setenv("VSYNC_CONFIG", filepath.Join(home, "env.yaml"))
	if got, err := resolveConfigPath(); err != nil || got != filepath.Join(home, "env.yaml") {
		t.Fatalf("resolveConfigPath() with env = (%q, %v), want env path", got, err)
	}
	t.Setenv("VSYNC_CONFIG", "")
	got, err := resolveConfigPath()
	if err != nil {
		t.Fatalf("resolveConfigPath() default error = %v", err)
	}
	want := filepath.Join(home, ".config", "vsync", "config.yaml")
	if got != want {
		t.Fatalf("resolveConfigPath() default = %q, want %q", got, want)
	}
}

func TestRootCmdRegistersCommandsAndFlags(t *testing.T) {
	root := rootCmd()
	if root.Use != "vsync" {
		t.Fatalf("root.Use = %q", root.Use)
	}
	if !root.SilenceUsage {
		t.Fatal("root.SilenceUsage = false, want true")
	}
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"init", "shell", "exec", "sync", "status", "cache"} {
		if !got[want] {
			t.Fatalf("root command missing %q", want)
		}
	}
	for _, want := range []string{"vault-addr", "vault-token", "config", "key"} {
		if root.PersistentFlags().Lookup(want) == nil {
			t.Fatalf("missing flag %q", want)
		}
	}
}

func TestPersistentPreRunSetsGlobalsForNonInit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dirs.Keys, "default.key")
	if _, err := crypto.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	flagKeyPath = ""
	globalDirs = nil
	globalKey = nil
	defer func() {
		flagKeyPath = ""
		globalDirs = nil
		globalKey = nil
	}()

	root := rootCmd()
	status := &cobra.Command{Use: "status"}
	if err := root.PersistentPreRunE(status, nil); err != nil {
		t.Fatalf("PersistentPreRunE() error = %v", err)
	}
	if globalDirs == nil || globalDirs.Base != dirs.Base {
		t.Fatalf("globalDirs = %#v, want base %q", globalDirs, dirs.Base)
	}
	if len(globalKey) != 32 {
		t.Fatalf("globalKey len = %d, want 32", len(globalKey))
	}
}

func TestPersistentPreRunSkipsInit(t *testing.T) {
	globalDirs = nil
	globalKey = nil
	defer func() {
		globalDirs = nil
		globalKey = nil
	}()
	root := rootCmd()
	initCmd := &cobra.Command{Use: "init"}
	if err := root.PersistentPreRunE(initCmd, nil); err != nil {
		t.Fatalf("PersistentPreRunE(init) error = %v", err)
	}
	if globalDirs != nil || globalKey != nil {
		t.Fatalf("init pre-run should not set globals, got %#v %#v", globalDirs, globalKey)
	}
}

func TestToSet(t *testing.T) {
	set := toSet([]string{"a", "b", "a"})
	if len(set) != 2 || !set["a"] || !set["b"] {
		t.Fatalf("toSet() = %#v", set)
	}
}
