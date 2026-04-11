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

const cacheMargin = 10 * time.Second

// Credentials holds the decrypted Vault connection details.
type Credentials struct {
	Addr  string
	Token string
}

// LoadCredentials decrypts VAULT_ADDR and VAULT_TOKEN from the state directory.
// If addrOverride / tokenOverride are non-empty they are used instead.
func LoadCredentials(dirs *state.Dirs, key []byte, addrOverride, tokenOverride string) (*Credentials, error) {
	addr := addrOverride
	if addr == "" {
		raw, err := crypto.DecryptFile(key, dirs.TokenFile("vault_addr"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("vault credentials not found; run 'vsync init' first")
			}
			return nil, fmt.Errorf("decrypt vault_addr: %w", err)
		}
		addr = string(raw)
	}

	token := tokenOverride
	if token == "" {
		raw, err := crypto.DecryptFile(key, dirs.TokenFile("vault_token"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("vault credentials not found; run 'vsync init' first")
			}
			return nil, fmt.Errorf("decrypt vault_token: %w", err)
		}
		token = string(raw)
	}

	return &Credentials{Addr: addr, Token: token}, nil
}

// StoreCredentials encrypts and writes VAULT_ADDR and VAULT_TOKEN to the state directory.
func StoreCredentials(dirs *state.Dirs, key []byte, addr, token string) error {
	if err := crypto.EncryptFile(key, dirs.TokenFile("vault_addr"), []byte(addr)); err != nil {
		return fmt.Errorf("store vault_addr: %w", err)
	}
	if err := crypto.EncryptFile(key, dirs.TokenFile("vault_token"), []byte(token)); err != nil {
		return fmt.Errorf("store vault_token: %w", err)
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
	var secret *vaultapi.Secret
	var err error

	if c.kvVersion == 2 {
		secret, err = c.api.Logical().Read(path)
	} else {
		secret, err = c.api.Logical().Read(path)
	}
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
func GetCachedEnvSecret(dirs *state.Dirs, key []byte, client *Client, prefix, secretKey string) (string, error) {
	entry, _ := ReadCache(dirs, key, "env", secretKey)
	if entry != nil && !entry.IsExpired() {
		return entry.Value, nil
	}

	result, err := client.GetEnvSecret(prefix, secretKey)
	if err != nil {
		// Fall back to stale cache if Vault is unavailable.
		if entry != nil {
			fmt.Fprintf(os.Stderr, "vsync: vault unavailable, using stale cache for %s\n", secretKey)
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
	_ = WriteCache(dirs, key, "env", secretKey, newEntry)
	return result.Value, nil
}

// GetCachedFileSecret fetches from cache if valid, otherwise from Vault, and updates cache.
func GetCachedFileSecret(dirs *state.Dirs, key []byte, client *Client, prefix, secretKey string) (string, error) {
	entry, _ := ReadCache(dirs, key, "files", secretKey)
	if entry != nil && !entry.IsExpired() {
		return entry.Value, nil
	}

	result, err := client.GetFileSecret(prefix, secretKey)
	if err != nil {
		if entry != nil {
			fmt.Fprintf(os.Stderr, "vsync: vault unavailable, using stale cache for file %s\n", secretKey)
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
	_ = WriteCache(dirs, key, "files", secretKey, newEntry)
	return result.Value, nil
}
