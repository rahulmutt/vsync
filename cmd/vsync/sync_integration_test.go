//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

func requireDevenvVault(t *testing.T) (*vaultapi.Client, *state.Dirs, []byte) {
	t.Helper()
	addr := os.Getenv("VAULT_ADDR")
	token := os.Getenv("VAULT_TOKEN")
	if addr == "" || token == "" {
		t.Skip("VAULT_ADDR and VAULT_TOKEN are required; run via devenv shell")
	}

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
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nfiles:\n  - path: ~/synced.txt\n    key: example\n    mode: \"0640\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSYNC_CONFIG", cfgPath)

	if err := vlt.StoreCredentials(dirs, key, addr, token); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ExpandPaths(); err != nil {
		t.Fatal(err)
	}

	client, err := vaultapi.NewClient(vaultapi.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	client.SetAddress(addr)
	client.SetToken(token)
	return client, dirs, key
}

func TestSyncCmdAgainstDevenvVault(t *testing.T) {
	client, dirs, key := requireDevenvVault(t)
	secretKey := strings.ReplaceAll(t.Name(), "/", "-")

	cfgPath := os.Getenv("VSYNC_CONFIG")
	cfgContent := fmt.Sprintf("vault:\n  kv_version: 2\nfiles:\n  - path: ~/synced.txt\n    key: %s\n    mode: \"0640\"\n", secretKey)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Seed a sample file secret using the live Vault dev server.
	if _, err := client.Logical().Write("secret/data/vsync/files/"+secretKey, map[string]any{
		"data": map[string]any{"content": "hello from devenv"},
	}); err != nil {
		t.Fatalf("seed vault secret: %v", err)
	}

	flagConfigPath = cfgPath
	globalDirs = dirs
	globalKey = key
	defer func() {
		flagConfigPath = ""
		globalDirs = nil
		globalKey = nil
	}()

	cmd := syncCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("sync cmd failed: %v\noutput=%s", err, stdout.String())
	}

	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "synced.txt"))
	if err != nil {
		t.Fatalf("read synced file: %v\noutput=%s", err, stdout.String())
	}
	if string(data) != "hello from devenv" {
		t.Fatalf("synced file = %q, want %q", data, "hello from devenv")
	}

	entry, err := vlt.ReadCache(dirs, key, "files", secretKey)
	if err != nil || entry == nil {
		t.Fatalf("cache entry missing: %#v %v", entry, err)
	}
	if entry.Value != "hello from devenv" {
		t.Fatalf("cache value = %q", entry.Value)
	}
}

func TestCacheClearCmdAgainstDevenvVault(t *testing.T) {
	_, dirs, key := requireDevenvVault(t)
	if err := vlt.WriteCache(dirs, key, "env", "gemini-api-key", &vlt.CacheEntry{Value: "cached", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
	}()

	cmd := cacheCmd()
	cmd.SetArgs([]string{"clear", "--env"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cache clear failed: %v\noutput=%s", err, stdout.String())
	}
	if got, err := vlt.ReadCache(dirs, key, "env", "gemini-api-key"); err != nil || got != nil {
		t.Fatalf("cache not cleared: %#v %v", got, err)
	}
}
