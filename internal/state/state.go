// Package state manages ~/.local/state/vsync/* path helpers and atomic file I/O.
package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dirs holds the canonical paths under ~/.local/state/vsync.
type Dirs struct {
	Base   string // ~/.local/state/vsync
	Keys   string // .../keys
	Tokens string // .../tokens
	Cache  string // .../cache
	Shims  string // .../shims
}

var userHomeDirFn = os.UserHomeDir
var createTempFn = os.CreateTemp
var fileChmodFn = func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }
var fileWriteFn = func(f *os.File, data []byte) (int, error) { return f.Write(data) }
var fileCloseFn = func(f *os.File) error { return f.Close() }
var renameFn = os.Rename

// DefaultDirs returns the standard state directories, expanding $HOME.
func DefaultDirs() (*Dirs, error) {
	home, err := userHomeDirFn()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	base := filepath.Join(home, ".local", "state", "vsync")
	return &Dirs{
		Base:   base,
		Keys:   filepath.Join(base, "keys"),
		Tokens: filepath.Join(base, "tokens"),
		Cache:  filepath.Join(base, "cache"),
		Shims:  filepath.Join(base, "shims"),
	}, nil
}

// EnsureAll creates all state directories with mode 0700.
func (d *Dirs) EnsureAll() error {
	for _, dir := range []string{d.Keys, d.Tokens,
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

// CacheFile returns the path for an encrypted cache entry.
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
