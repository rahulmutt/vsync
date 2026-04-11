package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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
		flagConfigPath = ""
		flagVaultAddr = ""
		flagVaultToken = ""
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

	origLoad := loadVaultCredentials
	origClient := newVaultClient
	origSecret := getCachedEnvSecret
	origExec := execRealCommand
	defer func() {
		loadVaultCredentials = origLoad
		newVaultClient = origClient
		getCachedEnvSecret = origSecret
		execRealCommand = origExec
	}()

	loadVaultCredentials = func(d *state.Dirs, k []byte, addrOverride, tokenOverride string) (*vlt.Credentials, error) {
		if d != globalDirs || !reflect.DeepEqual(k, key) {
			t.Fatalf("loadVaultCredentials got wrong dirs or key")
		}
		if addrOverride != "http://addr" || tokenOverride != "token" {
			t.Fatalf("overrides = %q/%q, want http://addr/token", addrOverride, tokenOverride)
		}
		return &vlt.Credentials{Addr: addrOverride, Token: tokenOverride}, nil
	}
	newVaultClient = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
		if creds.Addr != "http://addr" || creds.Token != "token" || kvVersion != 2 {
			t.Fatalf("newVaultClient args = %#v, %d", creds, kvVersion)
		}
		return &vlt.Client{}, nil
	}
	getCachedEnvSecret = func(d *state.Dirs, k []byte, client *vlt.Client, prefix, secretKey string) (string, error) {
		if d != globalDirs || !reflect.DeepEqual(k, key) || client == nil {
			t.Fatal("getCachedEnvSecret got unexpected args")
		}
		if prefix != "secret/data/vsync/env" {
			t.Fatalf("prefix = %q", prefix)
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
