package vault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vsync/vsync/internal/crypto"
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

	if err := StoreCredentialsForProfile(dirs, key, "prod", "http://prod:8200", "prod-token"); err != nil {
		t.Fatalf("StoreCredentialsForProfile() error = %v", err)
	}
	prod, err := LoadCredentialsForProfile(dirs, key, "prod", "", "")
	if err != nil {
		t.Fatalf("LoadCredentialsForProfile() error = %v", err)
	}
	if prod.Addr != "http://prod:8200" || prod.Token != "prod-token" {
		t.Fatalf("LoadCredentialsForProfile() = %#v", prod)
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

func TestLoadCredentialsPartialOverrides(t *testing.T) {
	base := t.TempDir()
	dirs := &state.Dirs{Base: base, Keys: filepath.Join(base, "keys"), Tokens: filepath.Join(base, "tokens"), Cache: filepath.Join(base, "cache"), Shims: filepath.Join(base, "shims")}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if err := StoreCredentials(dirs, key, "http://stored:8200", "stored-token"); err != nil {
		t.Fatal(err)
	}
	if creds, err := LoadCredentials(dirs, key, "http://override:8200", ""); err != nil || creds.Addr != "http://override:8200" || creds.Token != "stored-token" {
		t.Fatalf("LoadCredentials(addr override) = %#v, %v", creds, err)
	}
	if creds, err := LoadCredentials(dirs, key, "", "override-token"); err != nil || creds.Addr != "http://stored:8200" || creds.Token != "override-token" {
		t.Fatalf("LoadCredentials(token override) = %#v, %v", creds, err)
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

func TestNewClientAndCredentialsErrorPaths(t *testing.T) {
	if _, err := NewClient(&Credentials{Addr: "://bad", Token: "token"}, 2); err == nil {
		t.Fatal("NewClient() error = nil, want parse error")
	}

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
	if err := StoreCredentials(dirs, key, "http://stored:8200", "stored-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(dirs, key[:16], "", ""); err == nil {
		t.Fatal("LoadCredentials() error = nil, want decrypt error")
	}

	badDirs := &state.Dirs{Base: base, Keys: dirs.Keys, Tokens: filepath.Join(base, "tokenfile"), Cache: dirs.Cache, Shims: dirs.Shims}
	if err := os.WriteFile(badDirs.Tokens, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := StoreCredentials(badDirs, key, "http://stored:8200", "stored-token"); err == nil {
		t.Fatal("StoreCredentials() error = nil, want mkdir failure")
	}

	fileDirs := &state.Dirs{Base: base, Keys: dirs.Keys, Tokens: filepath.Join(base, "tokens-file"), Cache: dirs.Cache, Shims: dirs.Shims}
	if err := os.WriteFile(fileDirs.Tokens, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := StoreCredentials(fileDirs, key, "http://stored:8200", "stored-token"); err == nil {
		t.Fatal("StoreCredentials() error = nil, want mkdir failure")
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

func TestTokenTTLUnlimitedAndStaleCacheFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/token/lookup-self", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{"lease_duration": 0}})
	})
	mux.HandleFunc("/v1/secret/data/vsync/env/gemini-api-key", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/v1/secret/data/vsync/files/example", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client, err := NewClient(&Credentials{Addr: server.URL, Token: "token"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ttl, err := client.TokenTTL(); err != nil || ttl != 0 {
		t.Fatalf("TokenTTL() = %v, %v, want 0, nil", ttl, err)
	}

	base := t.TempDir()
	dirs := &state.Dirs{Base: base, Keys: filepath.Join(base, "keys"), Tokens: filepath.Join(base, "tokens"), Cache: filepath.Join(base, "cache"), Shims: filepath.Join(base, "shims")}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if err := WriteCache(dirs, key, "env", "gemini-api-key", &CacheEntry{Value: "stale-env", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := WriteCache(dirs, key, "files", "example", &CacheEntry{Value: "stale-file", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got, err := GetCachedEnvSecret(dirs, key, client, "secret/data/vsync/env", "gemini-api-key"); err != nil || got != "stale-env" {
		t.Fatalf("GetCachedEnvSecret() stale fallback = %q, %v", got, err)
	}
	if got, err := GetCachedFileSecret(dirs, key, client, "secret/data/vsync/files", "example"); err != nil || got != "stale-file" {
		t.Fatalf("GetCachedFileSecret() stale fallback = %q, %v", got, err)
	}
}

func TestCredentialsDecryptAndStoreErrorPaths(t *testing.T) {
	base := t.TempDir()
	dirs := &state.Dirs{Base: base, Keys: filepath.Join(base, "keys"), Tokens: filepath.Join(base, "tokens"), Cache: filepath.Join(base, "cache"), Shims: filepath.Join(base, "shims")}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	if err := StoreCredentials(dirs, key, "http://stored", "stored-token"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(dirs.TokenFile("vault_addr"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(dirs, key, "", "stored-token"); err == nil {
		t.Fatal("LoadCredentials() addr error = nil, want decrypt error")
	}

	if err := StoreCredentials(dirs, key, "http://stored", "stored-token"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.TokenFile("vault_token"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(dirs, key, "http://stored", ""); err == nil {
		t.Fatal("LoadCredentials() token error = nil, want decrypt error")
	}

	origEncrypt := encryptFileFn
	defer func() { encryptFileFn = origEncrypt }()
	calls := 0
	encryptFileFn = func(key []byte, path string, plaintext []byte) error {
		calls++
		if calls == 1 {
			return origEncrypt(key, path, plaintext)
		}
		return os.ErrPermission
	}
	if err := StoreCredentials(dirs, key, "http://stored", "stored-token"); err == nil {
		t.Fatal("StoreCredentials() error = nil, want second write failure")
	}

	missingTokenBase := t.TempDir()
	missingTokenDirs := &state.Dirs{Base: missingTokenBase, Keys: filepath.Join(missingTokenBase, "keys"), Tokens: filepath.Join(missingTokenBase, "tokens"), Cache: filepath.Join(missingTokenBase, "cache"), Shims: filepath.Join(missingTokenBase, "shims")}
	if err := missingTokenDirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	if err := crypto.EncryptFile(key, missingTokenDirs.TokenFile("vault_addr"), []byte("http://stored")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(missingTokenDirs, key, "", ""); err == nil {
		t.Fatal("LoadCredentials() error = nil, want missing token error")
	}
}

func TestSecretReadAndLeaseBranches(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/token/lookup-self", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ttl": "bad-duration"}})
	})
	mux.HandleFunc("/v1/secret/data/vsync/env/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/secret/data/vsync/env/no-value", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]any{}, "metadata": map[string]any{}}})
	})
	mux.HandleFunc("/v1/secret/data/vsync/env/leased", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"lease_duration": 20, "data": map[string]any{"data": map[string]any{"value": "leased-env"}, "metadata": map[string]any{}}})
	})
	mux.HandleFunc("/v1/secret/data/vsync/files/no-content", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]any{"content": "file-content"}, "metadata": map[string]any{}}})
	})
	mux.HandleFunc("/v1/secret/data/vsync/files/leased", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"lease_duration": 20, "data": map[string]any{"data": map[string]any{"content": "leased-file"}, "metadata": map[string]any{}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client, err := NewClient(&Credentials{Addr: server.URL, Token: "token"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.TokenTTL(); err == nil {
		t.Fatal("TokenTTL() error = nil, want parse error")
	}
	if _, err := client.GetEnvSecret("secret/data/vsync/env", "missing"); err == nil {
		t.Fatal("GetEnvSecret() error = nil, want missing secret")
	}
	if _, err := client.GetEnvSecret("secret/data/vsync/env", "no-value"); err == nil {
		t.Fatal("GetEnvSecret() error = nil, want missing value")
	}

	base := t.TempDir()
	dirs := &state.Dirs{Base: base, Keys: filepath.Join(base, "keys"), Tokens: filepath.Join(base, "tokens"), Cache: filepath.Join(base, "cache"), Shims: filepath.Join(base, "shims")}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if got, err := GetCachedEnvSecret(dirs, key, client, "secret/data/vsync/env", "leased"); err != nil || got != "leased-env" {
		t.Fatalf("GetCachedEnvSecret() = %q, %v, want leased-env", got, err)
	}
	entry, err := ReadCache(dirs, key, "env", "leased")
	if err != nil || entry == nil || entry.ExpiresAt.IsZero() {
		t.Fatalf("expected cached env entry with expiry, got %#v, %v", entry, err)
	}
	if got, err := GetCachedFileSecret(dirs, key, client, "secret/data/vsync/files", "leased"); err != nil || got != "leased-file" {
		t.Fatalf("GetCachedFileSecret() = %q, %v, want leased-file", got, err)
	}
	fentry, err := ReadCache(dirs, key, "files", "leased")
	if err != nil || fentry == nil || fentry.ExpiresAt.IsZero() {
		t.Fatalf("expected cached file entry with expiry, got %#v, %v", fentry, err)
	}
}
