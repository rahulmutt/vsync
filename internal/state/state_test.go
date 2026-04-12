package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDirsUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("VSYNC_STATE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("VSYNC_CACHE_DIR", "")

	dirs, err := DefaultDirs()
	if err != nil {
		t.Fatalf("DefaultDirs() error = %v", err)
	}
	wantStateBase := filepath.Join(home, ".local", "state", "vsync")
	if dirs.Base != wantStateBase {
		t.Fatalf("Base = %q, want %q", dirs.Base, wantStateBase)
	}
	wantCacheBase := filepath.Join(home, ".cache", "vsync")
	if dirs.Cache != wantCacheBase {
		t.Fatalf("Cache = %q, want %q", dirs.Cache, wantCacheBase)
	}
	if got, want := dirs.KeyFile(), filepath.Join(wantStateBase, "keys", "default.key"); got != want {
		t.Fatalf("KeyFile() = %q, want %q", got, want)
	}
	if got, want := dirs.TokenFile("vault_addr"), filepath.Join(wantStateBase, "tokens", "vault_addr.enc"); got != want {
		t.Fatalf("TokenFile() = %q, want %q", got, want)
	}
	if got, want := dirs.CacheFile("env", "gemini"), filepath.Join(wantCacheBase, "env", "gemini.enc"); got != want {
		t.Fatalf("CacheFile() = %q, want %q", got, want)
	}
	if got, want := dirs.ShimFile("pi"), filepath.Join(wantStateBase, "shims", "pi"); got != want {
		t.Fatalf("ShimFile() = %q, want %q", got, want)
	}
}

func TestDefaultDirsUsesXDGStateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	xdgStateDir := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", xdgStateDir)
	t.Setenv("VSYNC_STATE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("VSYNC_CACHE_DIR", "")

	dirs, err := DefaultDirs()
	if err != nil {
		t.Fatalf("DefaultDirs() error = %v", err)
	}
	wantBase := filepath.Join(xdgStateDir, "vsync")
	if dirs.Base != wantBase {
		t.Fatalf("Base = %q, want %q", dirs.Base, wantBase)
	}
	if got, want := dirs.KeyFile(), filepath.Join(wantBase, "keys", "default.key"); got != want {
		t.Fatalf("KeyFile() = %q, want %q", got, want)
	}
}

func TestDefaultDirsUsesXDGCacheDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("VSYNC_STATE_DIR", "")
	xdgCacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("XDG_CACHE_HOME", xdgCacheDir)
	t.Setenv("VSYNC_CACHE_DIR", "")

	dirs, err := DefaultDirs()
	if err != nil {
		t.Fatalf("DefaultDirs() error = %v", err)
	}
	wantCache := filepath.Join(xdgCacheDir, "vsync")
	if dirs.Cache != wantCache {
		t.Fatalf("Cache = %q, want %q", dirs.Cache, wantCache)
	}
	if got, want := dirs.CacheFile("files", "pi-agent-auth"), filepath.Join(wantCache, "files", "pi-agent-auth.enc"); got != want {
		t.Fatalf("CacheFile() = %q, want %q", got, want)
	}
}

func TestDefaultDirsUsesVsyncCacheDirOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("VSYNC_STATE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "xdg-cache"))
	vsyncCacheDir := filepath.Join(t.TempDir(), "custom-vsync-cache")
	t.Setenv("VSYNC_CACHE_DIR", vsyncCacheDir)

	dirs, err := DefaultDirs()
	if err != nil {
		t.Fatalf("DefaultDirs() error = %v", err)
	}
	if dirs.Cache != vsyncCacheDir {
		t.Fatalf("Cache = %q, want %q", dirs.Cache, vsyncCacheDir)
	}
	if got, want := dirs.CacheFile("env", "gemini"), filepath.Join(vsyncCacheDir, "env", "gemini.enc"); got != want {
		t.Fatalf("CacheFile() = %q, want %q", got, want)
	}
}

func TestDefaultDirsUsesVsyncStateDirOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "xdg-state"))
	vsyncStateDir := filepath.Join(t.TempDir(), "custom-vsync-state")
	t.Setenv("VSYNC_STATE_DIR", vsyncStateDir)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("VSYNC_CACHE_DIR", "")

	dirs, err := DefaultDirs()
	if err != nil {
		t.Fatalf("DefaultDirs() error = %v", err)
	}
	if dirs.Base != vsyncStateDir {
		t.Fatalf("Base = %q, want %q", dirs.Base, vsyncStateDir)
	}
	if got, want := dirs.Cache, filepath.Join(home, ".cache", "vsync"); got != want {
		t.Fatalf("Cache = %q, want %q", got, want)
	}
	if got, want := dirs.CacheFile("env", "gemini"), filepath.Join(home, ".cache", "vsync", "env", "gemini.enc"); got != want {
		t.Fatalf("CacheFile() = %q, want %q", got, want)
	}
}

func TestEnsureAllCreatesDirectories(t *testing.T) {
	base := t.TempDir()
	dirs := &Dirs{
		Base:   filepath.Join(base, "base"),
		Keys:   filepath.Join(base, "keys"),
		Tokens: filepath.Join(base, "tokens"),
		Cache:  filepath.Join(base, "cache"),
		Shims:  filepath.Join(base, "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatalf("EnsureAll() error = %v", err)
	}
	for _, dir := range []string{dirs.Cache, dirs.Keys, dirs.Tokens, filepath.Join(dirs.Cache, "env"), filepath.Join(dirs.Cache, "files"), dirs.Shims} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
		if got, want := info.Mode().Perm(), os.FileMode(0700); got != want {
			t.Fatalf("perm for %s = %v, want %v", dir, got, want)
		}
	}

	dirs.Keys = filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(dirs.Keys, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err == nil {
		t.Fatal("EnsureAll() error = nil, want mkdir failure")
	}
}

func TestWriteAtomicWritesContentAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "file.txt")
	if err := WriteAtomic(path, []byte("hello"), 0600); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want %q", data, "hello")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0600); got != want {
		t.Fatalf("perm = %v, want %v", got, want)
	}
}

func TestDefaultDirsAndWriteAtomicErrorPaths(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("VSYNC_STATE_DIR", "")
	origHome := userHomeDirFn
	userHomeDirFn = func() (string, error) { return "", errors.New("no home") }
	defer func() { userHomeDirFn = origHome }()
	if _, err := DefaultDirs(); err == nil || err.Error() != "cannot determine home directory: no home" {
		t.Fatalf("DefaultDirs() error = %v, want wrapped no home", err)
	}

	// The explicit state/cache overrides should bypass the home lookup entirely.
	t.Setenv("VSYNC_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	t.Setenv("VSYNC_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	if dirs, err := DefaultDirs(); err != nil {
		t.Fatalf("DefaultDirs() with overrides error = %v", err)
	} else if dirs.Base != os.Getenv("VSYNC_STATE_DIR") || dirs.Cache != os.Getenv("VSYNC_CACHE_DIR") {
		t.Fatalf("DefaultDirs() with overrides = %#v", dirs)
	}

	// Trigger the cache-side home-directory lookup error separately.
	t.Setenv("VSYNC_STATE_DIR", filepath.Join(t.TempDir(), "state2"))
	t.Setenv("VSYNC_CACHE_DIR", "")
	if _, err := DefaultDirs(); err == nil || err.Error() != "cannot determine home directory: no home" {
		t.Fatalf("DefaultDirs() cache error = %v, want wrapped no home", err)
	}

	// Reset env for the WriteAtomic error-path checks below.
	t.Setenv("VSYNC_STATE_DIR", "")
	t.Setenv("VSYNC_CACHE_DIR", "")

	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.WriteFile(parent, []byte("not a dir"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(filepath.Join(parent, "child.txt"), []byte("x"), 0600); err == nil {
		t.Fatal("WriteAtomic() error = nil, want mkdir failure")
	}

	origCreateTemp, origRename := createTempFn, renameFn
	origChmod, origWrite, origClose := fileChmodFn, fileWriteFn, fileCloseFn
	defer func() {
		createTempFn = origCreateTemp
		renameFn = origRename
		fileChmodFn = origChmod
		fileWriteFn = origWrite
		fileCloseFn = origClose
	}()
	createTempFn = func(string, string) (*os.File, error) { return nil, errors.New("temp fail") }
	if err := WriteAtomic(filepath.Join(t.TempDir(), "temp.txt"), []byte("x"), 0600); err == nil || err.Error() != "create temp: temp fail" {
		t.Fatalf("WriteAtomic() create temp error = %v, want temp fail", err)
	}
	createTempFn = origCreateTemp
	fileChmodFn = func(*os.File, os.FileMode) error { return errors.New("chmod fail") }
	if err := WriteAtomic(filepath.Join(t.TempDir(), "chmod.txt"), []byte("x"), 0600); err == nil || err.Error() != "chmod fail" {
		t.Fatalf("WriteAtomic() chmod error = %v, want chmod fail", err)
	}
	fileChmodFn = origChmod
	fileWriteFn = func(*os.File, []byte) (int, error) { return 0, errors.New("write fail") }
	if err := WriteAtomic(filepath.Join(t.TempDir(), "write.txt"), []byte("x"), 0600); err == nil || err.Error() != "write fail" {
		t.Fatalf("WriteAtomic() write error = %v, want write fail", err)
	}
	fileWriteFn = origWrite
	fileCloseFn = func(*os.File) error { return errors.New("close fail") }
	if err := WriteAtomic(filepath.Join(t.TempDir(), "close.txt"), []byte("x"), 0600); err == nil || err.Error() != "close fail" {
		t.Fatalf("WriteAtomic() close error = %v, want close fail", err)
	}
	fileCloseFn = origClose
	renameFn = func(string, string) error { return errors.New("rename fail") }
	if err := WriteAtomic(filepath.Join(t.TempDir(), "rename.txt"), []byte("x"), 0600); err == nil || err.Error() != "rename fail" {
		t.Fatalf("WriteAtomic() rename error = %v, want rename fail", err)
	}
}
