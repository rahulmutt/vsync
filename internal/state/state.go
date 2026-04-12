// Package state manages state path helpers and atomic file I/O.
package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dirs holds the canonical paths under the vsync state and cache directories.
type Dirs struct {
	Base   string // ~/.local/state/vsync, $XDG_STATE_DIR/vsync, or $VSYNC_STATE_DIR
	Keys   string // .../keys
	Tokens string // .../tokens
	Cache  string // ~/.cache/vsync, $XDG_CACHE_DIR/vsync, or $VSYNC_CACHE_DIR
	Shims  string // .../shims
}

var userHomeDirFn = os.UserHomeDir
var createTempFn = os.CreateTemp
var fileChmodFn = func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }
var fileWriteFn = func(f *os.File, data []byte) (int, error) { return f.Write(data) }
var fileCloseFn = func(f *os.File) error { return f.Close() }
var renameFn = os.Rename

// DefaultDirs returns the standard state and cache directories, honoring
// VSYNC_STATE_DIR / XDG_STATE_DIR for state and VSYNC_CACHE_DIR / XDG_CACHE_DIR
// for cache, with home-directory fallbacks.
func DefaultDirs() (*Dirs, error) {
	stateBase := os.Getenv("VSYNC_STATE_DIR")
	if stateBase == "" {
		if xdgStateDir := os.Getenv("XDG_STATE_DIR"); xdgStateDir != "" {
			stateBase = filepath.Join(xdgStateDir, "vsync")
		} else {
			home, err := userHomeDirFn()
			if err != nil {
				return nil, fmt.Errorf("cannot determine home directory: %w", err)
			}
			stateBase = filepath.Join(home, ".local", "state", "vsync")
		}
	}

	cacheBase := os.Getenv("VSYNC_CACHE_DIR")
	if cacheBase == "" {
		if xdgCacheDir := os.Getenv("XDG_CACHE_DIR"); xdgCacheDir != "" {
			cacheBase = filepath.Join(xdgCacheDir, "vsync")
		} else {
			home, err := userHomeDirFn()
			if err != nil {
				return nil, fmt.Errorf("cannot determine home directory: %w", err)
			}
			cacheBase = filepath.Join(home, ".cache", "vsync")
		}
	}

	return &Dirs{
		Base:   stateBase,
		Keys:   filepath.Join(stateBase, "keys"),
		Tokens: filepath.Join(stateBase, "tokens"),
		Cache:  cacheBase,
		Shims:  filepath.Join(stateBase, "shims"),
	}, nil
}

// EnsureAll creates all state and cache directories with mode 0700.
func (d *Dirs) EnsureAll() error {
	for _, dir := range []string{d.Keys, d.Tokens, d.Cache,
		filepath.Join(d.Cache, "env"),
		filepath.Join(d.Cache, "files"),
		d.Shims,
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// KeyFile returns the path to the default encryption key file.
func (d *Dirs) KeyFile() string {
	return filepath.Join(d.Keys, "default.key")
}

// TokenFile returns the path for an encrypted token file.
func (d *Dirs) TokenFile(name string) string {
	return filepath.Join(d.Tokens, name+".enc")
}

// CacheFile returns the path for an encrypted cache entry under the cache dir.
func (d *Dirs) CacheFile(kind, name string) string {
	return filepath.Join(d.Cache, kind, name+".enc")
}

// ShimFile returns the path for a shim script.
func (d *Dirs) ShimFile(name string) string {
	return filepath.Join(d.Shims, name)
}

// WriteAtomic writes data to path atomically (temp file + rename) with the given mode.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := createTempFn(dir, ".vsync-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // clean up if rename fails

	if err := fileChmodFn(tmp, mode); err != nil {
		_ = fileCloseFn(tmp)
		return err
	}
	if _, err := fileWriteFn(tmp, data); err != nil {
		_ = fileCloseFn(tmp)
		return err
	}
	if err := fileCloseFn(tmp); err != nil {
		return err
	}
	return renameFn(tmpName, path)
}
