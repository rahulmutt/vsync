package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vsync/vsync/internal/state"
)

func TestClearCacheKindIgnoresMissingDirectory(t *testing.T) {
	dirs := &state.Dirs{
		Base:   t.TempDir(),
		Keys:   filepath.Join(t.TempDir(), "keys"),
		Tokens: filepath.Join(t.TempDir(), "tokens"),
		Cache:  filepath.Join(t.TempDir(), "cache"),
		Shims:  filepath.Join(t.TempDir(), "shims"),
	}
	if err := ClearCacheKind(dirs, "env"); err != nil {
		t.Fatalf("ClearCacheKind() error = %v", err)
	}
}

func TestDeleteCacheIgnoresMissingFile(t *testing.T) {
	dirs := &state.Dirs{
		Base:   t.TempDir(),
		Keys:   filepath.Join(t.TempDir(), "keys"),
		Tokens: filepath.Join(t.TempDir(), "tokens"),
		Cache:  filepath.Join(t.TempDir(), "cache"),
		Shims:  filepath.Join(t.TempDir(), "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	if err := DeleteCache(dirs, "env", "missing"); err != nil {
		t.Fatalf("DeleteCache() missing error = %v", err)
	}
}

func TestReadCacheMissingReturnsNil(t *testing.T) {
	dirs := &state.Dirs{
		Base:   t.TempDir(),
		Keys:   filepath.Join(t.TempDir(), "keys"),
		Tokens: filepath.Join(t.TempDir(), "tokens"),
		Cache:  filepath.Join(t.TempDir(), "cache"),
		Shims:  filepath.Join(t.TempDir(), "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCache(dirs, testKey(t), "env", "missing"); err != nil || got != nil {
		t.Fatalf("ReadCache() missing = (%#v, %v), want (nil, nil)", got, err)
	}
}

func TestWriteCacheCreatesParentDirectories(t *testing.T) {
	dirs := &state.Dirs{
		Base:   t.TempDir(),
		Keys:   filepath.Join(t.TempDir(), "keys"),
		Tokens: filepath.Join(t.TempDir(), "tokens"),
		Cache:  filepath.Join(t.TempDir(), "cache"),
		Shims:  filepath.Join(t.TempDir(), "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := testKey(t)
	if err := WriteCache(dirs, key, "env", "nested/name", &CacheEntry{Value: "x"}); err != nil {
		t.Fatalf("WriteCache() error = %v", err)
	}
	if _, err := os.Stat(dirs.CacheFile("env", "nested/name")); err != nil {
		t.Fatalf("cache file missing: %v", err)
	}
}
