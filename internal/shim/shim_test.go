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

	if err := Remove(dirs, "pi"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(dirs.ShimFile("pi")); !os.IsNotExist(err) {
		t.Fatalf("shim still exists after Remove; err=%v", err)
	}
}
