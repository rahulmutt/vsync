package vault

import (
	"errors"
	"fmt"
	"os"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
)

var (
	decryptFileFn = crypto.DecryptFile
	encryptFileFn = crypto.EncryptFile
)

const cacheMargin = 10 * time.Second

// Credentials holds the decrypted Vault connection details.
type Credentials struct {
	Addr  string
	Token string
}

// LoadCredentials decrypts VAULT_ADDR and VAULT_TOKEN from the state directory.
// If addrOverride / tokenOverride are non-empty they are used instead.
func LoadCredentials(dirs *state.Dirs, key []byte, addrOverride, tokenOverride string) (*Credentials, error) {
	return LoadCredentialsForProfile(dirs, key, "default", addrOverride, tokenOverride)
}

// LoadCredentialsForProfile decrypts the credentials for a given profile.
func LoadCredentialsForProfile(dirs *state.Dirs, key []byte, profile, addrOverride, tokenOverride string) (*Credentials, error) {
	addr := addrOverride
	if addr == "" {
		raw, err := decryptFileFn(key, dirs.ProfileTokenFile(profile, "vault_addr"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("vault credentials for profile %q not found; run 'vsync init' first", profile)
			}
			return nil, fmt.Errorf("decrypt vault_addr for profile %q: %w", profile, err)
		}
		addr = string(raw)
	}

	token := tokenOverride
	if token == "" {
		raw, err := decryptFileFn(key, dirs.ProfileTokenFile(profile, "vault_token"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("vault credentials for profile %q not found; run 'vsync init' first", profile)
			}
			return nil, fmt.Errorf("decrypt vault_token for profile %q: %w", profile, err)
		}
		token = string(raw)
	}

	return &Credentials{Addr: addr, Token: token}, nil
}

// StoreCredentials encrypts and writes VAULT_ADDR and VAULT_TOKEN to the state directory.
func StoreCredentials(dirs *state.Dirs, key []byte, addr, token string) error {
	return StoreCredentialsForProfile(dirs, key, "default", addr, token)
}

// StoreCredentialsForProfile encrypts and writes credentials for a profile.
func StoreCredentialsForProfile(dirs *state.Dirs, key []byte, profile, addr, token string) error {
	if err := encryptFileFn(key, dirs.ProfileTokenFile(profile, "vault_addr"), []byte(addr)); err != nil {
		return fmt.Errorf("store vault_addr for profile %q: %w", profile, err)
	}
	if err := encryptFileFn(key, dirs.ProfileTokenFile(profile, "vault_token"), []byte(token)); err != nil {
		return fmt.Errorf("store vault_token for profile %q: %w", profile, err)
	}
	return nil
}

// Client wraps the HashiCorp Vault API client.
type Client struct {
	api       *vaultapi.Client
	kvVersion int
}

// NewClient creates a new Vault client from the given credentials.
func NewClient(creds *Credentials, kvVersion int) (*Client, error) {
	cfg := vaultapi.DefaultConfig()
	cfg.Address = creds.Addr
	c, err := vaultapi.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("vault client: %w", err)
	}
	c.SetToken(creds.Token)
	return &Client{api: c, kvVersion: kvVersion}, nil
}

// Ping verifies connectivity by calling the token self-lookup endpoint.
func (c *Client) Ping() error {
	_, err := c.api.Auth().Token().LookupSelf()
	if err != nil {
		return fmt.Errorf("vault ping: %w", err)
	}
	return nil
}

// TokenTTL returns the remaining TTL of the current token, or 0 if unlimited.
func (c *Client) TokenTTL() (time.Duration, error) {
	secret, err := c.api.Auth().Token().LookupSelf()
	if err != nil {
		return 0, fmt.Errorf("lookup self: %w", err)
	}
	ttl, err := secret.TokenTTL()
	if err != nil {
		return 0, err
	}
	return ttl, nil
}

// secretResult holds a fetched secret value and its lease duration.
type secretResult struct {
	Value         string
	LeaseDuration time.Duration
}

// GetEnvSecret fetches a secret from the env prefix path.
// For KV v2, it reads the `value` field from `data`.
// For KV v1, it reads the `value` field directly.
func (c *Client) GetEnvSecret(prefix, key string) (*secretResult, error) {
	path := prefix + "/" + key
	return c.readSecret(path, "value")
}

// GetFileSecret fetches a secret from the files prefix path, reading the `content` field.
func (c *Client) GetFileSecret(prefix, key string) (*secretResult, error) {
	path := prefix + "/" + key
	return c.readSecret(path, "content")
}

func (c *Client) readSecret(path, field string) (*secretResult, error) {
	secret, err := c.api.Logical().Read(path)
	if err != nil {
		return nil, fmt.Errorf("vault read %s: %w", path, err)
	}
	if secret == nil {
		return nil, fmt.Errorf("vault secret not found: %s", path)
	}

	data := secret.Data
	// KV v2 wraps data inside a "data" key.
	if c.kvVersion == 2 {
		if inner, ok := secret.Data["data"]; ok {
			if m, ok := inner.(map[string]interface{}); ok {
				data = m
			}
		}
	}

	raw, ok := data[field]
	if !ok {
		return nil, fmt.Errorf("vault secret %s: field %q not found", path, field)
	}
	value, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("vault secret %s: field %q is not a string", path, field)
	}

	leaseDur := time.Duration(secret.LeaseDuration) * time.Second
	return &secretResult{Value: value, LeaseDuration: leaseDur}, nil
}

// GetCachedEnvSecret fetches from cache if valid, otherwise from Vault, and updates cache.
// Backwards compatibility: GetCachedEnvSecret(..., prefix, secretKey) uses the
// default profile cache. Pass an additional profile name as the final optional
// argument to scope the cache entry to a profile-specific source.
func GetCachedEnvSecret(dirs *state.Dirs, key []byte, client *Client, prefix, secretKey string, profile ...string) (string, error) {
	cacheProfile := "default"
	if len(profile) > 0 && profile[0] != "" {
		cacheProfile = profile[0]
	}
	entry, _ := ReadCache(dirs, key, "env", cacheProfile, secretKey)
	if entry != nil && !entry.IsExpired() {
		return entry.Value, nil
	}

	result, err := client.GetEnvSecret(prefix, secretKey)
	if err != nil {
		// Fall back to stale cache if Vault is unavailable.
		if entry != nil {
			fmt.Fprintf(os.Stderr, "vsync: vault unavailable, using stale cache for %s (%s)\n", secretKey, cacheProfile)
			return entry.Value, nil
		}
		return "", err
	}

	newEntry := &CacheEntry{
		Value:     result.Value,
		VaultPath: prefix + "/" + secretKey,
	}
	if result.LeaseDuration > 0 {
		newEntry.ExpiresAt = time.Now().Add(result.LeaseDuration - cacheMargin)
	}
	_ = WriteCache(dirs, key, "env", cacheProfile, secretKey, newEntry)
	return result.Value, nil
}

// GetCachedFileSecret fetches from cache if valid, otherwise from Vault, and updates cache.
// Backwards compatibility: GetCachedFileSecret(..., prefix, secretKey) uses the
// default profile cache. Pass an additional profile name as the final optional
// argument to scope the cache entry to a profile-specific source.
func GetCachedFileSecret(dirs *state.Dirs, key []byte, client *Client, prefix, secretKey string, profile ...string) (string, error) {
	cacheProfile := "default"
	if len(profile) > 0 && profile[0] != "" {
		cacheProfile = profile[0]
	}
	entry, _ := ReadCache(dirs, key, "files", cacheProfile, secretKey)
	if entry != nil && !entry.IsExpired() {
		return entry.Value, nil
	}

	result, err := client.GetFileSecret(prefix, secretKey)
	if err != nil {
		if entry != nil {
			fmt.Fprintf(os.Stderr, "vsync: vault unavailable, using stale cache for file %s (%s)\n", secretKey, cacheProfile)
			return entry.Value, nil
		}
		return "", err
	}

	newEntry := &CacheEntry{
		Value:     result.Value,
		VaultPath: prefix + "/" + secretKey,
	}
	if result.LeaseDuration > 0 {
		newEntry.ExpiresAt = time.Now().Add(result.LeaseDuration - cacheMargin)
	}
	_ = WriteCache(dirs, key, "files", cacheProfile, secretKey, newEntry)
	return result.Value, nil
}
