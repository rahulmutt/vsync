package vault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/vsync/vsync/internal/state"
)

func newMockVaultServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/token/lookup-self", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id": "req",
			"auth": map[string]any{
				"lease_duration": 42,
			},
		})
	})
	mux.HandleFunc("/v1/secret/data/vsync/env/gemini-api-key", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"data":     map[string]any{"value": "from-kv2"},
				"metadata": map[string]any{},
			},
		})
	})
	mux.HandleFunc("/v1/secret/data/vsync/files/example", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"data":     map[string]any{"content": "file-content"},
				"metadata": map[string]any{},
			},
		})
	})
	return httptest.NewServer(mux)
}

func TestNewClientPingAndTokenTTL(t *testing.T) {
	server := newMockVaultServer(t)
	defer server.Close()

	client, err := NewClient(&Credentials{Addr: server.URL, Token: "token"}, 2)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := client.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	ttl, err := client.TokenTTL()
	if err != nil {
		t.Fatalf("TokenTTL() error = %v", err)
	}
	if ttl != 42*time.Second {
		t.Fatalf("TokenTTL() = %v, want 42s", ttl)
	}
}

func TestGetEnvAndFileSecretKV2(t *testing.T) {
	server := newMockVaultServer(t)
	defer server.Close()

	client, err := NewClient(&Credentials{Addr: server.URL, Token: "token"}, 2)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if got, err := client.GetEnvSecret("secret/data/vsync/env", "gemini-api-key"); err != nil || got.Value != "from-kv2" {
		t.Fatalf("GetEnvSecret() = %#v, %v", got, err)
	}
	if got, err := client.GetFileSecret("secret/data/vsync/files", "example"); err != nil || got.Value != "file-content" {
		t.Fatalf("GetFileSecret() = %#v, %v", got, err)
	}
}

func TestGetEnvAndFileSecretKV1AndErrorPaths(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/token/lookup-self", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{"lease_duration": 1}})
	})
	mux.HandleFunc("/v1/secret/env/key", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"value": "flat-env"})
	})
	mux.HandleFunc("/v1/secret/file/key", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"content": 123})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client, err := NewClient(&Credentials{Addr: server.URL, Token: "token"}, 1)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if got, err := client.GetEnvSecret("secret/env", "key"); err != nil || got.Value != "flat-env" {
		t.Fatalf("KV1 GetEnvSecret() = %#v, %v", got, err)
	}
	if _, err := client.GetFileSecret("secret/file", "key"); err == nil {
		t.Fatal("GetFileSecret() error = nil, want type error")
	}
}

func TestPingAndTokenTTLErrorPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := NewClient(&Credentials{Addr: server.URL, Token: "token"}, 2)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := client.Ping(); err == nil {
		t.Fatal("Ping() error = nil, want error")
	}
	if _, err := client.TokenTTL(); err == nil {
		t.Fatal("TokenTTL() error = nil, want error")
	}
}

func TestLoadAndStoreCredentials(t *testing.T) {
	base := t.TempDir()
	dirs := &state.Dirs{
		Base:   base,
		Keys:   filepath.Join(base, "keys"),
		Tokens: filepath.Join(base, "tokens"),
		Cache:  filepath.Join(base, "cache"),
		Shims:  filepath.Join(base, "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(31 - i)
	}
	if err := StoreCredentials(dirs, key, "http://vault:8200", "tok"); err != nil {
		t.Fatalf("StoreCredentials() error = %v", err)
	}
	creds, err := LoadCredentials(dirs, key, "", "")
	if err != nil {
		t.Fatalf("LoadCredentials() error = %v", err)
	}
	if creds.Addr != "http://vault:8200" || creds.Token != "tok" {
		t.Fatalf("LoadCredentials() = %#v", creds)
	}
}

func TestCachedSecretsUseFreshCacheAndFallbackOnVaultError(t *testing.T) {
	base := t.TempDir()
	dirs := &state.Dirs{
		Base:   base,
		Keys:   filepath.Join(base, "keys"),
		Tokens: filepath.Join(base, "tokens"),
		Cache:  filepath.Join(base, "cache"),
		Shims:  filepath.Join(base, "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	server := newMockVaultServer(t)
	defer server.Close()
	client, err := NewClient(&Credentials{Addr: server.URL, Token: "token"}, 2)
	if err != nil {
		t.Fatal(err)
	}

	got, err := GetCachedEnvSecret(dirs, key, client, "secret/data/vsync/env", "gemini-api-key")
	if err != nil {
		t.Fatalf("GetCachedEnvSecret() error = %v", err)
	}
	if got != "from-kv2" {
		t.Fatalf("GetCachedEnvSecret() = %q, want %q", got, "from-kv2")
	}

	entry, err := ReadCache(dirs, key, "env", "gemini-api-key")
	if err != nil || entry == nil {
		t.Fatalf("ReadCache() = %#v, %v", entry, err)
	}
	entry.ExpiresAt = time.Now().Add(time.Hour)
	if err := WriteCache(dirs, key, "env", "gemini-api-key", entry); err != nil {
		t.Fatal(err)
	}

	badClient, err := NewClient(&Credentials{Addr: "http://127.0.0.1:1", Token: "token"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := GetCachedEnvSecret(dirs, key, badClient, "secret/data/vsync/env", "gemini-api-key"); err != nil || got != "from-kv2" {
		t.Fatalf("GetCachedEnvSecret() with fresh cache = %q, %v", got, err)
	}
}

func TestLoadCredentialsPrefersOverridesAndReportsMissing(t *testing.T) {
	base := t.TempDir()
	dirs := &state.Dirs{
		Base:   base,
		Keys:   filepath.Join(base, "keys"),
		Tokens: filepath.Join(base, "tokens"),
		Cache:  filepath.Join(base, "cache"),
		Shims:  filepath.Join(base, "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(17 + i)
	}

	if err := StoreCredentials(dirs, key, "http://stored:8200", "stored-token"); err != nil {
		t.Fatal(err)
	}
	creds, err := LoadCredentials(dirs, key, "http://override:8200", "override-token")
	if err != nil {
		t.Fatalf("LoadCredentials() with overrides error = %v", err)
	}
	if creds.Addr != "http://override:8200" || creds.Token != "override-token" {
		t.Fatalf("LoadCredentials() with overrides = %#v", creds)
	}

	missingBase := t.TempDir()
	missingDirs := &state.Dirs{
		Base:   missingBase,
		Keys:   filepath.Join(missingBase, "keys"),
		Tokens: filepath.Join(missingBase, "tokens"),
		Cache:  filepath.Join(missingBase, "cache"),
		Shims:  filepath.Join(missingBase, "shims"),
	}
	if _, err := LoadCredentials(missingDirs, key, "", ""); err == nil {
		t.Fatal("LoadCredentials() error = nil, want missing credentials error")
	}
}

func TestGetCachedFileSecretUsesFreshCacheAndFallsBackOnError(t *testing.T) {
	base := t.TempDir()
	dirs := &state.Dirs{
		Base:   base,
		Keys:   filepath.Join(base, "keys"),
		Tokens: filepath.Join(base, "tokens"),
		Cache:  filepath.Join(base, "cache"),
		Shims:  filepath.Join(base, "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(31 - i)
	}

	server := newMockVaultServer(t)
	defer server.Close()
	client, err := NewClient(&Credentials{Addr: server.URL, Token: "token"}, 2)
	if err != nil {
		t.Fatal(err)
	}

	got, err := GetCachedFileSecret(dirs, key, client, "secret/data/vsync/files", "example")
	if err != nil {
		t.Fatalf("GetCachedFileSecret() error = %v", err)
	}
	if got != "file-content" {
		t.Fatalf("GetCachedFileSecret() = %q, want %q", got, "file-content")
	}

	entry, err := ReadCache(dirs, key, "files", "example")
	if err != nil || entry == nil {
		t.Fatalf("ReadCache() = %#v, %v", entry, err)
	}
	entry.ExpiresAt = time.Now().Add(time.Hour)
	if err := WriteCache(dirs, key, "files", "example", entry); err != nil {
		t.Fatal(err)
	}

	badClient, err := NewClient(&Credentials{Addr: "http://127.0.0.1:1", Token: "token"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := GetCachedFileSecret(dirs, key, badClient, "secret/data/vsync/files", "example"); err != nil || got != "file-content" {
		t.Fatalf("GetCachedFileSecret() with fresh cache = %q, %v", got, err)
	}
}

func TestCachedSecretsReturnErrorWithoutCacheWhenVaultFails(t *testing.T) {
	dirs := &state.Dirs{Base: t.TempDir(), Keys: filepath.Join(t.TempDir(), "keys"), Tokens: filepath.Join(t.TempDir(), "tokens"), Cache: filepath.Join(t.TempDir(), "cache"), Shims: filepath.Join(t.TempDir(), "shims")}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewClient(&Credentials{Addr: server.URL, Token: "token"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetCachedEnvSecret(dirs, key, client, "secret/data/vsync/env", "missing"); err == nil {
		t.Fatal("GetCachedEnvSecret() error = nil, want vault error")
	}
	if _, err := GetCachedFileSecret(dirs, key, client, "secret/data/vsync/files", "missing"); err == nil {
		t.Fatal("GetCachedFileSecret() error = nil, want vault error")
	}
}
