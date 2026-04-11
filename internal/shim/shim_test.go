package shim

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vsync/vsync/internal/state"
)

func TestEnsureListAndRemove(t *testing.T) {
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

	if err := Ensure(dirs, []string{"pi", "code"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(dirs.Shims, "nested"), 0700); err != nil {
		t.Fatalf("mkdir nested shim dir: %v", err)
	}

	for _, name := range []string{"pi", "code"} {
		path := dirs.ShimFile(name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", path, err)
		}
		if got, want := info.Mode().Perm(), os.FileMode(0755); got != want {
			t.Fatalf("perm for %s = %v, want %v", path, got, want)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		want := "#!/bin/sh\nexec vsync exec \"" + name + "\" \"$@\"\n"
		if string(data) != want {
			t.Fatalf("shim content = %q, want %q", data, want)
		}
	}

	names, err := List(dirs)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("List() len = %d, want 2", len(names))
	}
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	if !seen["pi"] || !seen["code"] {
		t.Fatalf("List() = %#v, want pi and code", names)
	}

	if err := Remove(dirs, "pi"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(dirs.ShimFile("pi")); !os.IsNotExist(err) {
		t.Fatalf("shim still exists after Remove; err=%v", err)
	}
	if err := Remove(dirs, "missing"); err != nil {
		t.Fatalf("Remove() missing error = %v", err)
	}

	missing := &state.Dirs{Shims: filepath.Join(t.TempDir(), "missing")}
	if names, err := List(missing); err != nil || names != nil {
		t.Fatalf("List() missing = (%#v, %v), want (nil, nil)", names, err)
	}

	fileDir := filepath.Join(base, "filedir")
	if err := os.WriteFile(fileDir, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if names, err := List(&state.Dirs{Shims: fileDir}); err == nil || names != nil {
		t.Fatalf("List() file dir = (%#v, %v), want error", names, err)
	}

	bad := &state.Dirs{Shims: filepath.Join(base, "shimfile")}
	if err := os.WriteFile(bad.Shims, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(bad, []string{"broken"}); err == nil {
		t.Fatal("Ensure() error = nil, want write failure")
	}
}
