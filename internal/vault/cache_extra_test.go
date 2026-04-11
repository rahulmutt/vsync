package vault

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vsync/vsync/internal/crypto"
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

func TestWriteCacheCreatesParentDirectoriesAndErrors(t *testing.T) {
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

	badParent := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(badParent, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	badDirs := &state.Dirs{Base: t.TempDir(), Keys: dirs.Keys, Tokens: dirs.Tokens, Cache: badParent, Shims: dirs.Shims}
	if err := WriteCache(badDirs, key, "env", "bad", &CacheEntry{Value: "x"}); err == nil {
		t.Fatal("WriteCache() error = nil, want mkdir failure")
	}
}

func TestReadCacheInvalidJSONAndMarshalError(t *testing.T) {
	dirs := &state.Dirs{Base: t.TempDir(), Keys: filepath.Join(t.TempDir(), "keys"), Tokens: filepath.Join(t.TempDir(), "tokens"), Cache: filepath.Join(t.TempDir(), "cache"), Shims: filepath.Join(t.TempDir(), "shims")}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key := testKey(t)
	if err := crypto.EncryptFile(key, dirs.CacheFile("env", "badjson"), []byte("{not-json")); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCache(dirs, key, "env", "badjson"); err != nil || got != nil {
		t.Fatalf("ReadCache() invalid json = (%#v, %v), want (nil, nil)", got, err)
	}

	origMarshal := jsonMarshalFn
	defer func() { jsonMarshalFn = origMarshal }()
	jsonMarshalFn = func(any) ([]byte, error) { return nil, errors.New("marshal") }
	if err := WriteCache(dirs, key, "env", "marshal", &CacheEntry{Value: "x"}); err == nil {
		t.Fatal("WriteCache() error = nil, want marshal failure")
	}
}

func TestClearCacheKindReturnsReadDirError(t *testing.T) {
	base := t.TempDir()
	dirs := &state.Dirs{Base: base, Keys: filepath.Join(base, "keys"), Tokens: filepath.Join(base, "tokens"), Cache: filepath.Join(base, "cache"), Shims: filepath.Join(base, "shims")}
	if err := os.MkdirAll(dirs.Cache, 0700); err != nil {
		t.Fatal(err)
	}
	cacheFile := filepath.Join(dirs.Cache, "env")
	if err := os.WriteFile(cacheFile, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ClearCacheKind(dirs, "env"); err == nil {
		t.Fatal("ClearCacheKind() error = nil, want read-dir failure")
	}
}
