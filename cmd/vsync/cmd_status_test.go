package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/shim"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

func setupStatusTest(t *testing.T) (*state.Dirs, []byte, string) {
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

func captureStdoutStderr(t *testing.T, fn func()) string {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wOut
	os.Stderr = wErr
	defer func() {
		os.Stdout = oldOut
		os.Stderr = oldErr
	}()

	outC := make(chan string, 1)
	errC := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(rOut)
		outC <- string(data)
	}()
	go func() {
		data, _ := io.ReadAll(rErr)
		errC <- string(data)
	}()

	fn()
	_ = wOut.Close()
	_ = wErr.Close()
	return <-outC + <-errC
}

func TestStatusCmdReportsConfiguredState(t *testing.T) {
	dirs, key, cfgPath := setupStatusTest(t)
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nenv:\n  commands:\n    - name: pi\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\nfiles:\n  - path: ~/missing.txt\n    key: notes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := shim.Ensure(dirs, []string{"pi"}); err != nil {
		t.Fatal(err)
	}
	if err := vlt.WriteCache(dirs, key, "env", "gemini-api-key", &vlt.CacheEntry{Value: "cached", ExpiresAt: time.Now().Add(time.Hour), VaultPath: "secret/data/vsync/env/gemini-api-key"}); err != nil {
		t.Fatal(err)
	}
	if err := vlt.WriteCache(dirs, key, "files", "notes", &vlt.CacheEntry{Value: "file", ExpiresAt: time.Now().Add(-time.Minute), VaultPath: "secret/data/vsync/files/notes"}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/token/lookup-self" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"auth":{"lease_duration":90}}`)
	}))
	defer server.Close()
	if err := vlt.StoreCredentials(dirs, key, server.URL, "token"); err != nil {
		t.Fatal(err)
	}
	flagVaultAddr = server.URL
	flagVaultToken = "token"

	cmd := statusCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	out := captureStdoutStderr(t, func() {
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("statusCmd() error = %v", err)
		}
	})
	for _, want := range []string{
		"=== vsync status ===",
		"Vault address:",
		"Token TTL:",
		"Configured commands (1):",
		"pi",
		"(shim present)",
		"GEMINI_API_KEY = gemini-api-key",
		"cached (expires in",
		"File sync entries (1):",
		"notes",
		"missing",
		"cached (expired)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestStatusCmdReportsMissingCredentials(t *testing.T) {
	_, _, cfgPath := setupStatusTest(t)
	if err := os.WriteFile(cfgPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := statusCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	out := captureStdoutStderr(t, func() {
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("statusCmd() error = %v", err)
		}
	})
	if !strings.Contains(out, "Credentials:") || !strings.Contains(out, "NOT FOUND") {
		t.Fatalf("status output missing credentials warning:\n%s", out)
	}
}
