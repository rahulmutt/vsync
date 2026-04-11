// Package vault provides Vault connectivity and secret caching.
package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
)

var jsonMarshalFn = json.Marshal

// CacheEntry is the JSON structure stored inside an encrypted cache file.
type CacheEntry struct {
	Value     string    `json:"value"`
	ExpiresAt time.Time `json:"expires_at"` // zero means never
	VaultPath string    `json:"vault_path"`
}

// IsExpired reports whether the cache entry has passed its expiry time.
// Entries with zero ExpiresAt never expire.
func (e *CacheEntry) IsExpired() bool {
	if e.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(e.ExpiresAt)
}

// ReadCache loads and decrypts a cache entry. Returns (nil, nil) if not found.
func ReadCache(dirs *state.Dirs, key []byte, kind, name string) (*CacheEntry, error) {
	path := dirs.CacheFile(kind, name)
	plain, err := crypto.DecryptFile(key, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		// Treat corrupted/unreadable cache as a miss (not a fatal error).
		return nil, nil
	}
	var entry CacheEntry
	if err := json.Unmarshal(plain, &entry); err != nil {
		return nil, nil
	}
	return &entry, nil
}

// WriteCache encrypts and writes a cache entry to disk.
func WriteCache(dirs *state.Dirs, key []byte, kind, name string, entry *CacheEntry) error {
	data, err := jsonMarshalFn(entry)
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	return crypto.EncryptFile(key, dirs.CacheFile(kind, name), data)
}

// DeleteCache removes a specific cache entry.
func DeleteCache(dirs *state.Dirs, kind, name string) error {
	path := dirs.CacheFile(kind, name)
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ClearCacheKind removes all cache entries of the given kind ("env" or "files").
func ClearCacheKind(dirs *state.Dirs, kind string) error {
	dir := dirs.CacheFile(kind, "")
	// CacheFile appends .enc to name; for the dir we need to strip trailing .enc
	// Actually use the Cache dir directly.
	cacheDir := fmt.Sprintf("%s/%s", dirs.Cache, kind)
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	_ = dir
	for _, e := range entries {
		_ = os.Remove(fmt.Sprintf("%s/%s", cacheDir, e.Name()))
	}
	return nil
}
