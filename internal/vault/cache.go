// Package vault provides Vault connectivity and secret caching.
package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// Backwards compatibility: ReadCache(dirs, key, kind, name) reads the default
// profile cache, while ReadCache(dirs, key, kind, profile, name) reads a
// profile-specific cache entry.
func ReadCache(dirs *state.Dirs, key []byte, kind string, parts ...string) (*CacheEntry, error) {
	path := dirs.CacheFile(kind, parts...)
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
// Backwards compatibility: WriteCache(dirs, key, kind, name, entry) writes to
// the default profile cache, while WriteCache(dirs, key, kind, profile, name,
// entry) writes to a profile-specific cache entry.
func WriteCache(dirs *state.Dirs, key []byte, kind string, partsAndEntry ...any) error {
	if len(partsAndEntry) < 2 {
		return fmt.Errorf("write cache: missing cache name")
	}
	entry, ok := partsAndEntry[len(partsAndEntry)-1].(*CacheEntry)
	if !ok {
		return fmt.Errorf("write cache: last argument must be *CacheEntry")
	}
	nameParts := partsAndEntry[:len(partsAndEntry)-1]
	parts := make([]string, 0, len(nameParts))
	for _, part := range nameParts {
		s, ok := part.(string)
		if !ok {
			return fmt.Errorf("write cache: cache path parts must be strings")
		}
		parts = append(parts, s)
	}
	data, err := jsonMarshalFn(entry)
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	return crypto.EncryptFile(key, dirs.CacheFile(kind, parts...), data)
}

// DeleteCache removes a specific cache entry.
func DeleteCache(dirs *state.Dirs, kind string, parts ...string) error {
	path := dirs.CacheFile(kind, parts...)
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// DeleteCacheAllProfiles removes cache entries for a given key across all profiles.
func DeleteCacheAllProfiles(dirs *state.Dirs, kind, name string) error {
	if err := DeleteCache(dirs, kind, name); err != nil {
		return err
	}
	cacheDir := filepath.Join(dirs.Cache, kind)
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, prof := range entries {
		if !prof.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(cacheDir, prof.Name(), name+".enc"))
	}
	return nil
}

// ClearCacheKind removes all cache entries of the given kind ("env" or "files").
func ClearCacheKind(dirs *state.Dirs, kind string) error {
	cacheDir := filepath.Join(dirs.Cache, kind)
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(cacheDir, e.Name()))
	}
	return nil
}
