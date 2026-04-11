package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

func setupCacheTest(t *testing.T) (*state.Dirs, []byte) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VSYNC_KEY", "")

	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	t.Cleanup(func() {
		globalDirs = nil
		globalKey = nil
	})
	return dirs, key
}

func TestCacheClearCmdClearsSpecificEntryAndKinds(t *testing.T) {
	dirs, key := setupCacheTest(t)
	if err := vlt.WriteCache(dirs, key, "env", "one", &vlt.CacheEntry{Value: "env-one"}); err != nil {
		t.Fatal(err)
	}
	if err := vlt.WriteCache(dirs, key, "files", "two", &vlt.CacheEntry{Value: "files-two"}); err != nil {
		t.Fatal(err)
	}

	cmd := cacheCmd()
	cmd.SetArgs([]string{"clear", "--key", "one"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("cache clear --key error = %v\noutput=%s", err, stdout.String())
	}
	if got, err := vlt.ReadCache(dirs, key, "env", "one"); err != nil || got != nil {
		t.Fatalf("env cache not cleared: %#v %v", got, err)
	}
	if got, err := vlt.ReadCache(dirs, key, "files", "two"); err != nil || got == nil {
		t.Fatalf("files cache unexpectedly changed: %#v %v", got, err)
	}

	if err := vlt.WriteCache(dirs, key, "env", "one", &vlt.CacheEntry{Value: "env-one"}); err != nil {
		t.Fatal(err)
	}
	cmd = cacheCmd()
	cmd.SetArgs([]string{"clear", "--files", "--key", "two"})
	stdout.Reset()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("cache clear --files --key error = %v\noutput=%s", err, stdout.String())
	}
	if got, err := vlt.ReadCache(dirs, key, "files", "two"); err != nil || got != nil {
		t.Fatalf("files cache not cleared: %#v %v", got, err)
	}
	if got, err := vlt.ReadCache(dirs, key, "env", "one"); err != nil || got == nil {
		t.Fatalf("env cache unexpectedly changed: %#v %v", got, err)
	}

	if err := vlt.WriteCache(dirs, key, "env", "one", &vlt.CacheEntry{Value: "env-one"}); err != nil {
		t.Fatal(err)
	}
	if err := vlt.WriteCache(dirs, key, "files", "two", &vlt.CacheEntry{Value: "files-two"}); err != nil {
		t.Fatal(err)
	}
	cmd = cacheCmd()
	cmd.SetArgs([]string{"clear", "--all"})
	stdout.Reset()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("cache clear --all error = %v\noutput=%s", err, stdout.String())
	}
	if got, err := vlt.ReadCache(dirs, key, "env", "one"); err != nil || got != nil {
		t.Fatalf("env cache not cleared by --all: %#v %v", got, err)
	}
	if got, err := vlt.ReadCache(dirs, key, "files", "two"); err != nil || got != nil {
		t.Fatalf("files cache not cleared by --all: %#v %v", got, err)
	}
}

func TestCacheClearCmdRequiresFlags(t *testing.T) {
	setupCacheTest(t)
	cmd := cacheCmd()
	cmd.SetArgs([]string{"clear"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("cache clear error = nil, want flag requirement error")
	}
}
