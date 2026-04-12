package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsync/vsync/internal/crypto"
	shellpkg "github.com/vsync/vsync/internal/shell"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

func setupShellTest(t *testing.T) (*state.Dirs, []byte, string) {
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
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nenv:\n  commands:\n    - name: pi\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\nfiles:\n  - path: ~/synced.txt\n    key: example\n    mode: \"0640\"\n"), 0600); err != nil {
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
		flagKeyPath = ""
		shellLaunchFn = shellpkg.Launch
	})
	return dirs, key, cfgPath
}

func TestShellCmdSyncsFilesEnsuresShimsAndLaunchesShell(t *testing.T) {
	dirs, key, cfgPath := setupShellTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/token/lookup-self":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"auth":{"lease_duration":3600}}`))
		case "/v1/secret/data/vsync/files/example":
			fmt.Fprint(w, `{"data":{"data":{"content":"hello from shell"},"metadata":{}}}`)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	if err := vlt.StoreCredentials(dirs, key, server.URL, "token"); err != nil {
		t.Fatal(err)
	}
	flagVaultAddr = server.URL
	flagVaultToken = "token"
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nenv:\n  commands:\n    - name: pi\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\nfiles:\n  - path: ~/synced.txt\n    key: example\n    mode: \"0640\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	called := false
	shellLaunchFn = func(shellBin, shimDir, keyFile string) error {
		called = true
		if shellBin != "/bin/zsh" {
			t.Fatalf("shellBin = %q, want /bin/zsh", shellBin)
		}
		if shimDir != dirs.Shims {
			t.Fatalf("shimDir = %q, want %q", shimDir, dirs.Shims)
		}
		if keyFile != dirs.KeyFile() {
			t.Fatalf("keyFile = %q, want %q", keyFile, dirs.KeyFile())
		}
		return context.Canceled
	}

	cmd := shellCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--shell", "/bin/zsh"})
	if err := cmd.ExecuteContext(context.Background()); err != context.Canceled {
		t.Fatalf("shellCmd() error = %v, want context.Canceled", err)
	}
	if !called {
		t.Fatal("shellLaunchFn was not called")
	}
	if data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "synced.txt")); err != nil {
		t.Fatalf("synced file missing: %v", err)
	} else if string(data) != "hello from shell" {
		t.Fatalf("synced file = %q, want %q", data, "hello from shell")
	}
	if _, err := os.Stat(dirs.ShimFile("pi")); err != nil {
		t.Fatalf("shim missing: %v", err)
	}
}

func TestShellCmdRejectsNestedShell(t *testing.T) {
	setupShellTest(t)
	t.Setenv("VSYNC_ACTIVE", "1")
	cmd := shellCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "nested shells are not supported") {
		t.Fatalf("shellCmd() error = %v, want nested shell error", err)
	}
}
