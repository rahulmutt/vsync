package vault

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func TestCacheRoundTripAndClear(t *testing.T) {
	base := t.TempDir()
	dirs := &state.Dirs{
		Base:   base,
		Keys:   filepath.Join(base, "keys"),
		Tokens: filepath.Join(base, "tokens"),
		Cache:  filepath.Join(base, "cache"),
		Shims:  filepath.Join(base, "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatalf("EnsureAll() error = %v", err)
	}
	key := testKey(t)

	entry := &CacheEntry{Value: "abc", VaultPath: "secret/data/vsync/env/foo", ExpiresAt: time.Now().Add(time.Hour)}
	if err := WriteCache(dirs, key, "env", "foo", entry); err != nil {
		t.Fatalf("WriteCache() error = %v", err)
	}
	got, err := ReadCache(dirs, key, "env", "foo")
	if err != nil {
		t.Fatalf("ReadCache() error = %v", err)
	}
	if got == nil || got.Value != "abc" || got.VaultPath != entry.VaultPath {
		t.Fatalf("ReadCache() = %#v, want %#v", got, entry)
	}

	if err := DeleteCache(dirs, "env", "foo"); err != nil {
		t.Fatalf("DeleteCache() error = %v", err)
	}
	if got, err := ReadCache(dirs, key, "env", "foo"); err != nil || got != nil {
		t.Fatalf("ReadCache() after DeleteCache = (%#v, %v), want (nil, nil)", got, err)
	}

	if err := WriteCache(dirs, key, "env", "one", entry); err != nil {
		t.Fatal(err)
	}
	if err := WriteCache(dirs, key, "files", "two", entry); err != nil {
		t.Fatal(err)
	}
	if err := ClearCacheKind(dirs, "env"); err != nil {
		t.Fatalf("ClearCacheKind() error = %v", err)
	}
	if got, err := ReadCache(dirs, key, "env", "one"); err != nil || got != nil {
		t.Fatalf("env cache not cleared: (%#v, %v)", got, err)
	}
	if got, err := ReadCache(dirs, key, "files", "two"); err != nil || got == nil {
		t.Fatalf("files cache should remain: (%#v, %v)", got, err)
	}
}

func TestReadCacheTreatsCorruptionAsMiss(t *testing.T) {
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
	if err := os.WriteFile(dirs.CacheFile("env", "bad"), []byte("not encrypted"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCache(dirs, testKey(t), "env", "bad"); err != nil || got != nil {
		t.Fatalf("ReadCache() on corruption = (%#v, %v), want (nil, nil)", got, err)
	}
}

func TestIsExpired(t *testing.T) {
	if (&CacheEntry{}).IsExpired() {
		t.Fatal("zero expiry should never expire")
	}
	if !(&CacheEntry{ExpiresAt: time.Now().Add(-time.Minute)}).IsExpired() {
		t.Fatal("past expiry should be expired")
	}
}

func TestWriteCacheStoresValidJSONAfterDecrypt(t *testing.T) {
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
	key := testKey(t)
	entry := &CacheEntry{Value: "x", VaultPath: "p"}
	if err := WriteCache(dirs, key, "env", "name", entry); err != nil {
		t.Fatal(err)
	}
	plain, err := crypto.DecryptFile(key, dirs.CacheFile("env", "name"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded CacheEntry
	if err := json.Unmarshal(plain, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Value != "x" || decoded.VaultPath != "p" {
		t.Fatalf("decoded = %#v", decoded)
	}
}
