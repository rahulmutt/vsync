package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDirsUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dirs, err := DefaultDirs()
	if err != nil {
		t.Fatalf("DefaultDirs() error = %v", err)
	}
	wantBase := filepath.Join(home, ".local", "state", "vsync")
	if dirs.Base != wantBase {
		t.Fatalf("Base = %q, want %q", dirs.Base, wantBase)
	}
	if got, want := dirs.KeyFile(), filepath.Join(wantBase, "keys", "default.key"); got != want {
		t.Fatalf("KeyFile() = %q, want %q", got, want)
	}
	if got, want := dirs.TokenFile("vault_addr"), filepath.Join(wantBase, "tokens", "vault_addr.enc"); got != want {
		t.Fatalf("TokenFile() = %q, want %q", got, want)
	}
	if got, want := dirs.CacheFile("env", "gemini"), filepath.Join(wantBase, "cache", "env", "gemini.enc"); got != want {
		t.Fatalf("CacheFile() = %q, want %q", got, want)
	}
	if got, want := dirs.ShimFile("pi"), filepath.Join(wantBase, "shims", "pi"); got != want {
		t.Fatalf("ShimFile() = %q, want %q", got, want)
	}
}

func TestEnsureAllCreatesDirectories(t *testing.T) {
	dirs := &Dirs{
		Base:   filepath.Join(t.TempDir(), "base"),
		Keys:   filepath.Join(t.TempDir(), "keys"),
		Tokens: filepath.Join(t.TempDir(), "tokens"),
		Cache:  filepath.Join(t.TempDir(), "cache"),
		Shims:  filepath.Join(t.TempDir(), "shims"),
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatalf("EnsureAll() error = %v", err)
	}
	for _, dir := range []string{dirs.Keys, dirs.Tokens, filepath.Join(dirs.Cache, "env"), filepath.Join(dirs.Cache, "files"), dirs.Shims} {
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

func TestWriteAtomicFailsWhenParentPathIsAFile(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.WriteFile(parent, []byte("not a dir"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(filepath.Join(parent, "child.txt"), []byte("x"), 0600); err == nil {
		t.Fatal("WriteAtomic() error = nil, want mkdir failure")
	}
}
