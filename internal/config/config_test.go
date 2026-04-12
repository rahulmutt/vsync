package config

import (
	"errors"
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

func TestLoadOrEmptyMissingAndExistingFile(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWD) }()

	missing := filepath.Join(t.TempDir(), "missing.yaml")
	cfg, err := LoadOrEmpty(missing)
	if err != nil {
		t.Fatalf("LoadOrEmpty() missing error = %v", err)
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

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("files:\n  - path: ~/x\n    key: y\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadOrEmpty(path)
	if err != nil {
		t.Fatalf("LoadOrEmpty() existing error = %v", err)
	}
	if got, want := cfg.Files[0].Mode, "0600"; got != want {
		t.Fatalf("default file mode = %q, want %q", got, want)
	}
}

func TestLoadOrEmptyMergesHierarchy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	basePath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(basePath, []byte(`vault:
  env_prefix: base/env
  files_prefix: base/files
  kv_version: 1
env:
  commands:
    - name: pi
      variables:
        - name: GEMINI_API_KEY
          key: base-gemini
files:
  - path: ~/base.txt
    key: base-file
`), 0600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	child := filepath.Join(root, "child")
	grandchild := filepath.Join(child, "grandchild")
	if err := os.MkdirAll(grandchild, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vsync.yaml"), []byte(`vault:
  env_prefix: root/env
env:
  commands:
    - name: pi
      variables:
        - name: OPENAI_API_KEY
          key: root-openai
files:
  - path: ~/root.txt
    key: shared
  - path: ~/root-only.txt
    key: root-only
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "vsync.yaml"), []byte(`vault:
  files_prefix: child/files
env:
  commands:
    - name: pi
      variables:
        - name: GEMINI_API_KEY
          key: child-gemini
        - name: ANTHROPIC_API_KEY
          key: child-anthropic
files:
  - path: ~/child.txt
    key: shared
  - path: ~/child-only.txt
    key: child-only
`), 0600); err != nil {
		t.Fatal(err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(grandchild); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWD) }()

	cfg, err := LoadOrEmpty(basePath)
	if err != nil {
		t.Fatalf("LoadOrEmpty() merge error = %v", err)
	}
	if got, want := cfg.Vault.EnvPrefix, "root/env"; got != want {
		t.Fatalf("EnvPrefix = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.FilesPrefix, "child/files"; got != want {
		t.Fatalf("FilesPrefix = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.KVVersion, 1; got != want {
		t.Fatalf("KVVersion = %d, want %d", got, want)
	}
	if err := cfg.ExpandPaths(); err != nil {
		t.Fatalf("ExpandPaths() error = %v", err)
	}

	pi := cfg.FindCommand("pi")
	if pi == nil {
		t.Fatal("FindCommand(pi) = nil, want entry")
	}
	if got, want := len(pi.Variables), 3; got != want {
		t.Fatalf("pi variables = %d, want %d", got, want)
	}
	if got, want := pi.Variables[0], (VariableEntry{Name: "GEMINI_API_KEY", Key: "child-gemini"}); got != want {
		t.Fatalf("pi variable[0] = %#v, want %#v", got, want)
	}
	if got, want := pi.Variables[1], (VariableEntry{Name: "OPENAI_API_KEY", Key: "root-openai"}); got != want {
		t.Fatalf("pi variable[1] = %#v, want %#v", got, want)
	}
	if got, want := pi.Variables[2], (VariableEntry{Name: "ANTHROPIC_API_KEY", Key: "child-anthropic"}); got != want {
		t.Fatalf("pi variable[2] = %#v, want %#v", got, want)
	}

	if got, want := len(cfg.Files), 4; got != want {
		t.Fatalf("files len = %d, want %d", got, want)
	}
	if got, want := cfg.Files[0], (FileEntry{Path: filepath.Join(home, "base.txt"), Key: "base-file", Mode: "0600"}); got != want {
		t.Fatalf("files[0] = %#v, want %#v", got, want)
	}
	if got, want := cfg.Files[1], (FileEntry{Path: filepath.Join(home, "child.txt"), Key: "shared", Mode: "0600"}); got != want {
		t.Fatalf("files[1] = %#v, want %#v", got, want)
	}
	if got, want := cfg.Files[2], (FileEntry{Path: filepath.Join(home, "root-only.txt"), Key: "root-only", Mode: "0600"}); got != want {
		t.Fatalf("files[2] = %#v, want %#v", got, want)
	}
	if got, want := cfg.Files[3], (FileEntry{Path: filepath.Join(home, "child-only.txt"), Key: "child-only", Mode: "0600"}); got != want {
		t.Fatalf("files[3] = %#v, want %#v", got, want)
	}
}

func TestLoadReportsMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("Load() error = nil, want missing file error")
	}
}

func TestDefaultConfigPathUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath() error = %v", err)
	}
	want := filepath.Join(home, ".config", "vsync", "config.yaml")
	if got != want {
		t.Fatalf("DefaultConfigPath() = %q, want %q", got, want)
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

func TestExpandPathsHandlesTildeAndLeavesOtherPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &Config{Files: []FileEntry{{Path: "~", Key: "root"}, {Path: "~/notes.txt", Key: "notes"}, {Path: "relative.txt", Key: "rel"}, {Path: "/abs.txt", Key: "abs"}}}
	if err := cfg.ExpandPaths(); err != nil {
		t.Fatalf("ExpandPaths() error = %v", err)
	}
	if got, want := cfg.Files[0].Path, home; got != want {
		t.Fatalf("ExpandPaths(~) = %q, want %q", got, want)
	}
	if got, want := cfg.Files[1].Path, filepath.Join(home, "notes.txt"); got != want {
		t.Fatalf("ExpandPaths(~/*) = %q, want %q", got, want)
	}
	if got, want := cfg.Files[2].Path, "relative.txt"; got != want {
		t.Fatalf("ExpandPaths(relative) = %q, want %q", got, want)
	}
	if got, want := cfg.Files[3].Path, "/abs.txt"; got != want {
		t.Fatalf("ExpandPaths(abs) = %q, want %q", got, want)
	}
}

func TestDefaultConfigPathAndExpandPathsError(t *testing.T) {
	origHome := userHomeDirFn
	userHomeDirFn = func() (string, error) { return "", errors.New("no home") }
	defer func() { userHomeDirFn = origHome }()
	if _, err := DefaultConfigPath(); err == nil || err.Error() != "no home" {
		t.Fatalf("DefaultConfigPath() error = %v, want no home", err)
	}
	cfg := &Config{Files: []FileEntry{{Path: "~/x"}}}
	if err := cfg.ExpandPaths(); err == nil || err.Error() != "no home" {
		t.Fatalf("ExpandPaths() error = %v, want no home", err)
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
