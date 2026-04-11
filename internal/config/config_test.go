package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndExpandsPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`vault:
  kv_version: 0
env:
  commands:
    - name: pi
      variables:
        - name: GEMINI_API_KEY
          key: gemini-api-key
files:
  - path: ~/notes.txt
    key: notes
`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Vault.EnvPrefix, "secret/data/vsync/env"; got != want {
		t.Fatalf("EnvPrefix = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.FilesPrefix, "secret/data/vsync/files"; got != want {
		t.Fatalf("FilesPrefix = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.KVVersion, 2; got != want {
		t.Fatalf("KVVersion = %d, want %d", got, want)
	}
	if got, want := cfg.Files[0].Mode, "0600"; got != want {
		t.Fatalf("default file mode = %q, want %q", got, want)
	}

	if err := cfg.ExpandPaths(); err != nil {
		t.Fatalf("ExpandPaths() error = %v", err)
	}
	if got, want := cfg.Files[0].Path, filepath.Join(home, "notes.txt"); got != want {
		t.Fatalf("expanded path = %q, want %q", got, want)
	}
}

func TestLoadOrEmptyMissingFile(t *testing.T) {
	cfg, err := LoadOrEmpty(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadOrEmpty() error = %v", err)
	}
	if got, want := cfg.Vault.EnvPrefix, "secret/data/vsync/env"; got != want {
		t.Fatalf("EnvPrefix = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.FilesPrefix, "secret/data/vsync/files"; got != want {
		t.Fatalf("FilesPrefix = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.KVVersion, 2; got != want {
		t.Fatalf("KVVersion = %d, want %d", got, want)
	}
}

func TestFindCommand(t *testing.T) {
	cfg := &Config{Env: EnvConfig{Commands: []CommandEntry{{Name: "pi"}, {Name: "code"}}}}
	if got := cfg.FindCommand("code"); got == nil || got.Name != "code" {
		t.Fatalf("FindCommand(code) = %#v, want entry", got)
	}
	if got := cfg.FindCommand("missing"); got != nil {
		t.Fatalf("FindCommand(missing) = %#v, want nil", got)
	}
}

func TestLoadReportsParseError(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`vault: [`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load() error = nil, want parse error")
	}
}
