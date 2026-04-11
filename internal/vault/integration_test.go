//go:build integration

package vault

import (
	"os"
	"testing"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
)

func liveVaultClient(t *testing.T) (*vaultapi.Client, string, string) {
	t.Helper()
	addr := os.Getenv("VAULT_ADDR")
	token := os.Getenv("VAULT_TOKEN")
	if addr == "" || token == "" {
		t.Skip("VAULT_ADDR and VAULT_TOKEN are required; run via devenv shell")
	}
	return mustClient(t, addr, token), addr, token
}

func mustClient(t *testing.T, addr, token string) *vaultapi.Client {
	t.Helper()
	cfg := vaultapi.DefaultConfig()
	cfg.Address = addr
	c, err := vaultapi.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.SetToken(token)
	return c
}

func TestIntegrationCredentialsAndCacheAgainstDevenv(t *testing.T) {
	client, _, _ := liveVaultClient(t)

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

	if _, err := client.Logical().Write("secret/data/vsync/env/gemini-api-key", map[string]any{
		"data": map[string]any{"value": "integration-key"},
	}); err != nil {
		t.Fatalf("seed env secret: %v", err)
	}
	if _, err := client.Logical().Write("secret/data/vsync/files/example", map[string]any{
		"data": map[string]any{"content": "integration-file"},
	}); err != nil {
		t.Fatalf("seed file secret: %v", err)
	}

	creds := &Credentials{Addr: os.Getenv("VAULT_ADDR"), Token: os.Getenv("VAULT_TOKEN")}
	if err := StoreCredentials(dirs, key, creds.Addr, creds.Token); err != nil {
		t.Fatalf("StoreCredentials() error = %v", err)
	}
	loaded, err := LoadCredentials(dirs, key, "", "")
	if err != nil {
		t.Fatalf("LoadCredentials() error = %v", err)
	}
	if loaded.Addr != creds.Addr || loaded.Token != creds.Token {
		t.Fatalf("LoadCredentials() = %#v", loaded)
	}

	c, err := NewClient(loaded, 2)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := c.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if ttl, err := c.TokenTTL(); err != nil || ttl <= 0 {
		t.Fatalf("TokenTTL() = %v, %v", ttl, err)
	}

	got, err := GetCachedEnvSecret(dirs, key, c, "secret/data/vsync/env", "gemini-api-key")
	if err != nil {
		t.Fatalf("GetCachedEnvSecret() error = %v", err)
	}
	if got != "integration-key" {
		t.Fatalf("GetCachedEnvSecret() = %q, want %q", got, "integration-key")
	}

	// Force a stale cache and make sure we fall back when Vault is unreachable.
	entry := &CacheEntry{Value: "stale", ExpiresAt: time.Now().Add(-time.Minute), VaultPath: "secret/data/vsync/env/gemini-api-key"}
	if err := WriteCache(dirs, key, "env", "gemini-api-key", entry); err != nil {
		t.Fatal(err)
	}
	badClient := mustClient(t, "http://127.0.0.1:1", os.Getenv("VAULT_TOKEN"))
	if got, err := GetCachedEnvSecret(dirs, key, &Client{api: badClient, kvVersion: 2}, "secret/data/vsync/env", "gemini-api-key"); err != nil || got != "stale" {
		t.Fatalf("stale cache fallback = %q, %v", got, err)
	}

	if got, err := GetCachedFileSecret(dirs, key, c, "secret/data/vsync/files", "example"); err != nil || got != "integration-file" {
		t.Fatalf("GetCachedFileSecret() = %q, %v", got, err)
	}
}
