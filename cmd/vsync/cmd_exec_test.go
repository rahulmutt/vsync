package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

func setupExecTest(t *testing.T) (*state.Dirs, []byte, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VSYNC_KEY", "")

	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, ".config", "vsync", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSYNC_CONFIG", cfgPath)

	globalDirs = dirs
	globalKey = key
	t.Cleanup(func() {
		globalDirs = nil
		globalKey = nil
		flagGlobalConfigPath = ""
		flagConfigPath = ""
		flagVaultAddr = ""
		flagVaultToken = ""
		flagVaultEnvPrefix = ""
		flagVaultFilesPrefix = ""
		flagVaultKVVersion = ""
		flagKeyPath = ""
	})
	return dirs, key, cfgPath
}

func TestExecCmdDirectlyExecsUnknownCommand(t *testing.T) {
	dirs, _, _ := setupExecTest(t)

	called := false
	origExec := execRealCommand
	execRealCommand = func(name string, args []string, extraEnv map[string]string, shimDir string) error {
		called = true
		if name != "tool" {
			t.Fatalf("name = %q, want tool", name)
		}
		if !reflect.DeepEqual(args, []string{"a", "b"}) {
			t.Fatalf("args = %#v, want [a b]", args)
		}
		if extraEnv != nil {
			t.Fatalf("extraEnv = %#v, want nil", extraEnv)
		}
		if shimDir != dirs.Shims {
			t.Fatalf("shimDir = %q, want %q", shimDir, dirs.Shims)
		}
		return context.Canceled
	}
	defer func() { execRealCommand = origExec }()

	cmd := execCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"tool", "a", "b"})
	if err := cmd.ExecuteContext(context.Background()); err != context.Canceled {
		t.Fatalf("execCmd() error = %v, want context.Canceled", err)
	}
	if !called {
		t.Fatal("execRealCommand was not called")
	}
}

func TestExecCmdInjectsConfiguredEnv(t *testing.T) {
	_, key, cfgPath := setupExecTest(t)
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nenv:\n  commands:\n    - name: pi\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\n        - name: ANOTHER_KEY\n          key: another-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	flagVaultAddr = "http://addr"
	flagVaultToken = "token"

	origLoad := loadCredsFn
	origClient := newVaultClientFn
	origSecret := getCachedEnvSecret
	origExec := execRealCommand
	defer func() {
		loadCredsFn = origLoad
		newVaultClientFn = origClient
		getCachedEnvSecret = origSecret
		execRealCommand = origExec
	}()

	loadCredsFn = func(d *state.Dirs, k []byte, addrOverride, tokenOverride string) (*vlt.Credentials, error) {
		if d != globalDirs || !reflect.DeepEqual(k, key) {
			t.Fatalf("loadCredsFn got wrong dirs or key")
		}
		if addrOverride != "http://addr" || tokenOverride != "token" {
			t.Fatalf("overrides = %q/%q, want http://addr/token", addrOverride, tokenOverride)
		}
		return &vlt.Credentials{Addr: addrOverride, Token: tokenOverride}, nil
	}
	newVaultClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
		if creds.Addr != "http://addr" || creds.Token != "token" || kvVersion != 2 {
			t.Fatalf("newVaultClient args = %#v, %d", creds, kvVersion)
		}
		return &vlt.Client{}, nil
	}
	getCachedEnvSecret = func(d *state.Dirs, k []byte, client *vlt.Client, prefix, secretKey string, profile ...string) (string, error) {
		if d != globalDirs || !reflect.DeepEqual(k, key) || client == nil {
			t.Fatal("getCachedEnvSecret got unexpected args")
		}
		if prefix != "secret/data/vsync/env" {
			t.Fatalf("prefix = %q", prefix)
		}
		if len(profile) != 1 || profile[0] != "default" && profile[0] != "" {
			t.Fatalf("profile = %#v, want default/empty", profile)
		}
		if secretKey == "gemini-api-key" {
			return "gemini-value", nil
		}
		if secretKey == "another-key" {
			return "another-value", nil
		}
		t.Fatalf("unexpected secret key %q", secretKey)
		return "", nil
	}
	execRealCommand = func(name string, args []string, extraEnv map[string]string, shimDir string) error {
		if name != "pi" {
			t.Fatalf("name = %q, want pi", name)
		}
		if !reflect.DeepEqual(args, []string{"--flag"}) {
			t.Fatalf("args = %#v, want [--flag]", args)
		}
		want := map[string]string{"GEMINI_API_KEY": "gemini-value", "ANOTHER_KEY": "another-value"}
		if !reflect.DeepEqual(extraEnv, want) {
			t.Fatalf("extraEnv = %#v, want %#v", extraEnv, want)
		}
		if shimDir != globalDirs.Shims {
			t.Fatalf("shimDir = %q, want %q", shimDir, globalDirs.Shims)
		}
		return context.Canceled
	}

	if err := vlt.StoreCredentials(globalDirs, key, "http://addr", "token"); err != nil {
		t.Fatal(err)
	}

	cmd := execCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"pi", "--flag"})
	if err := cmd.ExecuteContext(context.Background()); err != context.Canceled {
		t.Fatalf("execCmd() error = %v, want context.Canceled", err)
	}
}

func TestExecCmdSkipsSecretsWhenFilterDoesNotMatch(t *testing.T) {
	_, key, cfgPath := setupExecTest(t)
	if err := os.WriteFile(cfgPath, []byte("env:\n  commands:\n    - name: pi\n      filter: args.exists(a, a == \"--with-secrets\")\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origLoad := loadCredsFn
	origClient := newVaultClientFn
	origSecret := getCachedEnvSecret
	origExec := execRealCommand
	defer func() {
		loadCredsFn = origLoad
		newVaultClientFn = origClient
		getCachedEnvSecret = origSecret
		execRealCommand = origExec
	}()

	loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) {
		t.Fatal("loadCredsFn should not be called when the filter does not match")
		return nil, nil
	}
	newVaultClientFn = func(*vlt.Credentials, int) (*vlt.Client, error) {
		t.Fatal("newVaultClientFn should not be called when the filter does not match")
		return nil, nil
	}
	getCachedEnvSecret = func(*state.Dirs, []byte, *vlt.Client, string, string, ...string) (string, error) {
		t.Fatal("getCachedEnvSecret should not be called when the filter does not match")
		return "", nil
	}
	execRealCommand = func(name string, args []string, extraEnv map[string]string, shimDir string) error {
		if name != "pi" {
			t.Fatalf("name = %q, want pi", name)
		}
		if !reflect.DeepEqual(args, []string{"--plain"}) {
			t.Fatalf("args = %#v, want [--plain]", args)
		}
		if extraEnv != nil {
			t.Fatalf("extraEnv = %#v, want nil", extraEnv)
		}
		if shimDir != globalDirs.Shims {
			t.Fatalf("shimDir = %q, want %q", shimDir, globalDirs.Shims)
		}
		return context.Canceled
	}

	if err := vlt.StoreCredentials(globalDirs, key, "http://addr", "token"); err != nil {
		t.Fatal(err)
	}

	cmd := execCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"pi", "--plain"})
	if err := cmd.ExecuteContext(context.Background()); err != context.Canceled {
		t.Fatalf("execCmd() error = %v, want context.Canceled", err)
	}
}

func TestExecCmdPropagatesConfigLoadError(t *testing.T) {
	setupExecTest(t)
	origLoad := loadConfigFn
	loadConfigFn = func(string, string) (*config.Config, error) { return nil, errors.New("cfg") }
	defer func() { loadConfigFn = origLoad }()

	cmd := execCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"tool"})
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "cfg") {
		t.Fatalf("execCmd() error = %v, want cfg error", err)
	}
}

func TestExecCmdPropagatesFilterError(t *testing.T) {
	_, key, cfgPath := setupExecTest(t)
	if err := os.WriteFile(cfgPath, []byte("env:\n  commands:\n    - name: pi\n      filter: args[\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	flagVaultAddr = "http://addr"
	flagVaultToken = "token"
	if err := vlt.StoreCredentials(globalDirs, key, "http://addr", "token"); err != nil {
		t.Fatal(err)
	}

	origExec := execRealCommand
	defer func() { execRealCommand = origExec }()
	execRealCommand = func(string, []string, map[string]string, string) error {
		t.Fatal("execRealCommand should not be called on filter error")
		return nil
	}

	cmd := execCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"pi", "--plain"})
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "evaluate filter for command \"pi\"") {
		t.Fatalf("execCmd() error = %v, want filter error", err)
	}
}
