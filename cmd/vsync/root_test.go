package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
)

func TestResolveHelpers(t *testing.T) {
	t.Setenv("VAULT_ADDR", "http://env-addr")
	t.Setenv("VAULT_TOKEN", "env-token")
	t.Setenv("VSYNC_CONFIG", "/tmp/config.yaml")
	t.Setenv("VSYNC_GLOBAL_CONFIG", "/tmp/global-config.yaml")

	flagVaultAddr = ""
	flagVaultToken = ""
	flagVaultEnvPrefix = ""
	flagVaultFilesPrefix = ""
	flagVaultKVVersion = ""
	flagGlobalConfigPath = ""
	flagConfigPath = ""
	flagKeyPath = ""
	defer func() {
		flagVaultAddr = ""
		flagVaultToken = ""
		flagVaultEnvPrefix = ""
		flagVaultFilesPrefix = ""
		flagVaultKVVersion = ""
		flagGlobalConfigPath = ""
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
	if got, err := resolveGlobalConfigPath(); err != nil || got != "/tmp/global-config.yaml" {
		t.Fatalf("resolveGlobalConfigPath() = (%q, %v)", got, err)
	}
}

func TestResolveDirsAndKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := resolveDirs()
	if err != nil {
		t.Fatalf("resolveDirs() error = %v", err)
	}
	if dirs.Base == "" {
		t.Fatal("resolveDirs() returned empty dirs")
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

func TestResolveConfigPathsUsesFlagEnvAndDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	flagGlobalConfigPath = filepath.Join(home, "global-flag.yaml")
	defer func() { flagGlobalConfigPath = "" }()
	if got, err := resolveGlobalConfigPath(); err != nil || got != flagGlobalConfigPath {
		t.Fatalf("resolveGlobalConfigPath() with flag = (%q, %v), want flag path", got, err)
	}
	flagGlobalConfigPath = ""
	t.Setenv("VSYNC_GLOBAL_CONFIG", filepath.Join(home, "global-env.yaml"))
	if got, err := resolveGlobalConfigPath(); err != nil || got != filepath.Join(home, "global-env.yaml") {
		t.Fatalf("resolveGlobalConfigPath() with env = (%q, %v), want env path", got, err)
	}
	t.Setenv("VSYNC_GLOBAL_CONFIG", "")
	got, err := resolveGlobalConfigPath()
	if err != nil {
		t.Fatalf("resolveGlobalConfigPath() default error = %v", err)
	}
	want := filepath.Join(home, ".config", "vsync", "config.yaml")
	if got != want {
		t.Fatalf("resolveGlobalConfigPath() default = %q, want %q", got, want)
	}

	flagConfigPath = filepath.Join(home, "override-flag.yaml")
	defer func() { flagConfigPath = "" }()
	if got, err := resolveConfigPath(); err != nil || got != flagConfigPath {
		t.Fatalf("resolveConfigPath() with flag = (%q, %v), want flag path", got, err)
	}
	flagConfigPath = ""
	t.Setenv("VSYNC_CONFIG", filepath.Join(home, "override-env.yaml"))
	if got, err := resolveConfigPath(); err != nil || got != filepath.Join(home, "override-env.yaml") {
		t.Fatalf("resolveConfigPath() with env = (%q, %v), want env path", got, err)
	}
	t.Setenv("VSYNC_CONFIG", "")
	got, err = resolveConfigPath()
	if err != nil {
		t.Fatalf("resolveConfigPath() default error = %v", err)
	}
	if got != "" {
		t.Fatalf("resolveConfigPath() default = %q, want empty", got)
	}
}

func TestResolveVaultOverrides(t *testing.T) {
	flagVaultEnvPrefix = "flag/env"
	flagVaultFilesPrefix = "flag/files"
	flagVaultKVVersion = "1"
	defer func() {
		flagVaultEnvPrefix = ""
		flagVaultFilesPrefix = ""
		flagVaultKVVersion = ""
	}()
	t.Setenv("VSYNC_VAULT_ENV_PREFIX", "env/env")
	t.Setenv("VSYNC_VAULT_FILES_PREFIX", "env/files")
	t.Setenv("VSYNC_VAULT_KV_VERSION", "2")

	cfg := &config.Config{}
	if err := applyVaultOverrides(cfg); err != nil {
		t.Fatalf("applyVaultOverrides() error = %v", err)
	}
	if got, want := cfg.Vault.EnvPrefix, "flag/env"; got != want {
		t.Fatalf("EnvPrefix = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.FilesPrefix, "flag/files"; got != want {
		t.Fatalf("FilesPrefix = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.KVVersion, 1; got != want {
		t.Fatalf("KVVersion = %d, want %d", got, want)
	}

	flagVaultEnvPrefix = ""
	flagVaultFilesPrefix = ""
	flagVaultKVVersion = ""
	cfg = &config.Config{}
	if err := applyVaultOverrides(cfg); err != nil {
		t.Fatalf("applyVaultOverrides() env error = %v", err)
	}
	if got, want := cfg.Vault.EnvPrefix, "env/env"; got != want {
		t.Fatalf("EnvPrefix(env) = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.FilesPrefix, "env/files"; got != want {
		t.Fatalf("FilesPrefix(env) = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.KVVersion, 2; got != want {
		t.Fatalf("KVVersion(env) = %d, want %d", got, want)
	}
}

func TestResolveVaultKVVersionRejectsInvalidValues(t *testing.T) {
	flagVaultKVVersion = "not-a-number"
	defer func() { flagVaultKVVersion = "" }()
	if _, err := resolveVaultKVVersion(); err == nil {
		t.Fatal("resolveVaultKVVersion() error = nil, want parse error")
	}
	flagVaultKVVersion = "3"
	if _, err := resolveVaultKVVersion(); err == nil {
		t.Fatal("resolveVaultKVVersion() error = nil, want invalid version error")
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
	for _, want := range []string{"vault-addr", "vault-token", "vault-env-prefix", "vault-files-prefix", "vault-kv-version", "global-config", "config", "key"} {
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
	flagVaultEnvPrefix = ""
	flagVaultFilesPrefix = ""
	flagVaultKVVersion = ""
	globalDirs = nil
	globalKey = nil
	defer func() {
		flagKeyPath = ""
		flagVaultEnvPrefix = ""
		flagVaultFilesPrefix = ""
		flagVaultKVVersion = ""
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

func TestRootCmdVersionAndHelp(t *testing.T) {
	root := rootCmd()
	if root.Version == "" {
		t.Fatal("root.Version is empty")
	}
	oldArgs := os.Args
	os.Args = []string{"vsync", "--version"}
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute(--version) error = %v", err)
	}
	os.Args = oldArgs
}

func TestPersistentPreRunResolveDirsError(t *testing.T) {
	orig := resolveDirsFn
	resolveDirsFn = func() (*state.Dirs, error) { return nil, errors.New("dirs") }
	defer func() { resolveDirsFn = orig }()
	root := rootCmd()
	status := &cobra.Command{Use: "status"}
	if err := root.PersistentPreRunE(status, nil); err == nil || !strings.Contains(err.Error(), "dirs") {
		t.Fatalf("PersistentPreRunE() error = %v, want dirs", err)
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

func TestDefaultConfigPathAndDieAndMain(t *testing.T) {
	origHome := userHomeDirFn
	userHomeDirFn = func() (string, error) { return "", errors.New("boom") }
	t.Setenv("XDG_CONFIG_HOME", "")
	if _, err := defaultGlobalConfigPath(); err == nil || err.Error() != "boom" {
		t.Fatalf("defaultGlobalConfigPath() error = %v, want boom", err)
	}
	userHomeDirFn = origHome

	var gotExit int
	origExit := exitFn
	exitFn = func(code int) { gotExit = code }
	defer func() { exitFn = origExit }()

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()
	die("hello %s", "world")
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if gotExit != 1 {
		t.Fatalf("die() exit = %d, want 1", gotExit)
	}
	if !bytes.Contains(out, []byte("vsync: hello world")) {
		t.Fatalf("die() stderr = %q, want prefix", out)
	}

	oldArgs := os.Args
	os.Args = []string{"vsync", "--help"}
	defer func() { os.Args = oldArgs }()
	main()
}

func TestToSet(t *testing.T) {
	set := toSet([]string{"a", "b", "a"})
	if len(set) != 2 || !set["a"] || !set["b"] {
		t.Fatalf("toSet() = %#v", set)
	}
}
