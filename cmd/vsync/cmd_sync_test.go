package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

func setupSyncTest(t *testing.T) (*state.Dirs, []byte, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

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

func TestSyncCmdSyncsFileAndWritesCache(t *testing.T) {
	dirs, key, cfgPath := setupSyncTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/secret/data/vsync/files/example" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":{"data":{"content":"hello from sync"},"metadata":{}}}`)
	}))
	defer server.Close()

	if err := vlt.StoreCredentials(dirs, key, server.URL, "token"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nfiles:\n  - path: ~/synced.txt\n    key: example\n    mode: \"0640\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := syncCmd()
	cmd.SetArgs(nil)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("syncCmd() error = %v\noutput=%s", err, stdout.String())
	}

	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "synced.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v\noutput=%s", err, stdout.String())
	}
	if string(data) != "hello from sync" {
		t.Fatalf("synced file = %q, want %q", data, "hello from sync")
	}
	if info, err := os.Stat(filepath.Join(os.Getenv("HOME"), "synced.txt")); err != nil {
		t.Fatal(err)
	} else if got, want := info.Mode().Perm(), os.FileMode(0640); got != want {
		t.Fatalf("synced file mode = %v, want %v", got, want)
	}
	entry, err := vlt.ReadCache(dirs, key, "files", "example")
	if err != nil || entry == nil {
		t.Fatalf("ReadCache() = %#v, %v", entry, err)
	}
	if entry.Value != "hello from sync" || entry.VaultPath != "secret/data/vsync/files/example" {
		t.Fatalf("cache entry = %#v", entry)
	}
}

func TestSyncCmdSkipsFreshCacheWithoutHittingVault(t *testing.T) {
	dirs, key, cfgPath := setupSyncTest(t)

	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nfiles:\n  - path: ~/cached.txt\n    key: cached\n    mode: \"0640\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "cached.txt"), []byte("old content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := vlt.WriteCache(dirs, key, "files", "cached", &vlt.CacheEntry{Value: "cached content", ExpiresAt: time.Now().Add(time.Hour), VaultPath: "secret/data/vsync/files/cached"}); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if err := vlt.StoreCredentials(dirs, key, server.URL, "token"); err != nil {
		t.Fatal(err)
	}

	cmd := syncCmd()
	cmd.SetArgs(nil)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("syncCmd() error = %v\noutput=%s", err, stdout.String())
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("Vault was hit %d times, want 0", got)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "cached.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old content" {
		t.Fatalf("cached file was rewritten: %q", data)
	}
}

func TestSyncCmdRejectsUnknownFileKey(t *testing.T) {
	dirs, key, cfgPath := setupSyncTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := vlt.StoreCredentials(dirs, key, server.URL, "token"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nfiles:\n  - path: ~/present.txt\n    key: present\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := syncCmd()
	cmd.SetArgs([]string{"--file", "missing"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("syncCmd() error = nil, want unknown file key error")
	} else if !strings.Contains(err.Error(), "no file entry with key \"missing\" found in config") {
		t.Fatalf("syncCmd() error = %v, want unknown file key error", err)
	}
}

func TestWriteFileRejectsInvalidMode(t *testing.T) {
	if err := writeFile(filepath.Join(t.TempDir(), "bad.txt"), "not-octal", []byte("x")); err == nil {
		t.Fatal("writeFile() error = nil, want invalid mode error")
	}
}

func TestSyncCmdForceBypassesFreshCache(t *testing.T) {
	dirs, key, cfgPath := setupSyncTest(t)
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nfiles:\n  - path: ~/forced.txt\n    key: forced\n    mode: \"0640\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "forced.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := vlt.WriteCache(dirs, key, "files", "forced", &vlt.CacheEntry{Value: "cached", ExpiresAt: time.Now().Add(time.Hour), VaultPath: "secret/data/vsync/files/forced"}); err != nil {
		t.Fatal(err)
	}

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprint(w, `{"data":{"data":{"content":"fresh"},"metadata":{}}}`)
	}))
	defer server.Close()
	if err := vlt.StoreCredentials(dirs, key, server.URL, "token"); err != nil {
		t.Fatal(err)
	}

	cmd := syncCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--force"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("syncCmd(--force) error = %v", err)
	}
	if hits != 1 {
		t.Fatalf("Vault hits = %d, want 1", hits)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "forced.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fresh" {
		t.Fatalf("forced sync file = %q, want fresh", data)
	}
}
