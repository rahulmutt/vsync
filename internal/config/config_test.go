package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	cfg, err := LoadOrEmpty(missing, "")
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
	cfg, err = LoadOrEmpty(missing, path)
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

	cfg, err := LoadOrEmpty(basePath, "")
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

func TestConfigPathsAndMergeConfigCoverage(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	grandchild := filepath.Join(child, "grandchild")
	if err := os.MkdirAll(grandchild, 0755); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(root, "vsync.yaml")
	childPath := filepath.Join(child, "vsync.yaml")
	grandchildPath := filepath.Join(grandchild, "vsync.yaml")
	for _, p := range []string{globalPath, childPath, grandchildPath} {
		if err := os.WriteFile(p, []byte("vault:\n  kv_version: 2\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	origWD := workingDirFn
	origAbs, origStat := absPathFn, statFn
	workingDirFn = func() (string, error) { return grandchild, nil }
	defer func() {
		workingDirFn = origWD
		absPathFn = origAbs
		statFn = origStat
	}()

	absPathFn = func(path string) (string, error) {
		if strings.Contains(path, "override-fail") {
			return "", errors.New("abs fail")
		}
		return filepath.Abs(path)
	}
	statFn = func(path string) (os.FileInfo, error) {
		if strings.Contains(path, "stat-fail") {
			return nil, errors.New("stat fail")
		}
		return os.Stat(path)
	}

	paths, err := configPaths(globalPath, globalPath)
	if err != nil {
		t.Fatalf("configPaths() error = %v", err)
	}
	want := []string{globalPath}
	if len(paths) != len(want) {
		t.Fatalf("configPaths() len = %d, want %d (%#v)", len(paths), len(want), paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("configPaths()[%d] = %q, want %q", i, paths[i], want[i])
		}
	}

	paths, err = configPaths(globalPath, "")
	if err != nil {
		t.Fatalf("configPaths() search error = %v", err)
	}
	want = []string{globalPath, childPath, grandchildPath}
	if len(paths) != len(want) {
		t.Fatalf("configPaths() search len = %d, want %d (%#v)", len(paths), len(want), paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("configPaths() search[%d] = %q, want %q", i, paths[i], want[i])
		}
	}

	overrideFail := filepath.Join(root, "override-fail")
	if err := os.WriteFile(overrideFail, []byte("vault:\n  kv_version: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths, err = configPaths(globalPath, overrideFail)
	if err != nil {
		t.Fatalf("configPaths() abs error = %v, want success", err)
	}
	if len(paths) != 2 || paths[1] != overrideFail {
		t.Fatalf("configPaths() abs error paths = %#v, want global + override", paths)
	}

	statFail := filepath.Join(root, "stat-fail")
	if err := os.WriteFile(statFail, []byte("vault:\n  kv_version: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := configPaths(globalPath, statFail); err == nil || !strings.Contains(err.Error(), "stat fail") {
		t.Fatalf("configPaths() stat error = %v, want stat fail", err)
	}

	cfg := &Config{Vault: VaultConfig{VaultProfileConfig: VaultProfileConfig{EnvPrefix: "keep"}}}
	mergeConfig(cfg, nil)
	if got, want := cfg.Vault.EnvPrefix, "keep"; got != want {
		t.Fatalf("mergeConfig(nil) mutated config: %q", got)
	}
}

func TestConfigPathsWorkingDirError(t *testing.T) {
	origWD := workingDirFn
	workingDirFn = func() (string, error) { return "", errors.New("wd fail") }
	defer func() { workingDirFn = origWD }()

	globalPath := filepath.Join(t.TempDir(), "vsync.yaml")
	if err := os.WriteFile(globalPath, []byte("vault:\n  kv_version: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := configPaths(globalPath, ""); err == nil || err.Error() != "wd fail" {
		t.Fatalf("configPaths() error = %v, want wd fail", err)
	}
}

func TestLoadOrEmptyErrorPaths(t *testing.T) {
	origWD := workingDirFn
	workingDirFn = func() (string, error) { return "", errors.New("load wd fail") }
	defer func() { workingDirFn = origWD }()

	globalPath := filepath.Join(t.TempDir(), "vsync.yaml")
	if err := os.WriteFile(globalPath, []byte("vault:\n  kv_version: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrEmpty("", ""); err == nil || !strings.Contains(err.Error(), "load wd fail") {
		t.Fatalf("LoadOrEmpty() error = %v, want load wd fail", err)
	}

	workingDirFn = origWD
	dir := t.TempDir()
	if _, err := LoadOrEmpty(dir, ""); err == nil {
		t.Fatal("LoadOrEmpty(directory) error = nil, want read error")
	}
}

func TestConfigPathsErrorBranches(t *testing.T) {
	origHome := userHomeDirFn
	origAbs, origStat := absPathFn, statFn
	defer func() {
		userHomeDirFn = origHome
		absPathFn = origAbs
		statFn = origStat
	}()

	userHomeDirFn = func() (string, error) { return "", errors.New("home fail") }
	if _, err := configPaths("", ""); err == nil || !strings.Contains(err.Error(), "home fail") {
		t.Fatalf("configPaths() home error = %v, want home fail", err)
	}

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	if err := os.WriteFile(globalPath, []byte("vault:\n  kv_version: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	absPathFn = func(path string) (string, error) {
		return filepath.Abs(path)
	}
	statFn = func(path string) (os.FileInfo, error) {
		if path == globalPath {
			return nil, errors.New("global stat fail")
		}
		return os.Stat(path)
	}
	if _, err := configPaths(globalPath, ""); err == nil || !strings.Contains(err.Error(), "global stat fail") {
		t.Fatalf("configPaths() global stat error = %v, want global stat fail", err)
	}

	statFn = func(path string) (os.FileInfo, error) {
		if strings.Contains(path, "vsync.yaml") {
			return nil, errors.New("local stat fail")
		}
		return os.Stat(path)
	}
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	localGlobal := filepath.Join(root, "global.yaml")
	if err := os.WriteFile(localGlobal, []byte("vault:\n  kv_version: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := configPaths(localGlobal, ""); err == nil || !strings.Contains(err.Error(), "local stat fail") {
		t.Fatalf("configPaths() local stat error = %v, want local stat fail", err)
	}
}

func TestDefaultConfigPathUsesXDGAndHomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg-config"))
	got, err := DefaultGlobalConfigPath()
	if err != nil {
		t.Fatalf("DefaultGlobalConfigPath() error = %v", err)
	}
	want := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "vsync", "config.yaml")
	if got != want {
		t.Fatalf("DefaultGlobalConfigPath() = %q, want %q", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	got, err = DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath() error = %v", err)
	}
	want = filepath.Join(home, ".config", "vsync", "config.yaml")
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

func TestLoadSupportsProfilesAndReferenceProfiles(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`vault:
  addr: http://default:8200
  token: default-token
  env_prefix: default/env
  files_prefix: default/files
  kv_version: 1
  profiles:
    prod:
      addr: http://prod:8200
      token: prod-token
      env_prefix: prod/env
      files_prefix: prod/files
      kv_version: 2
env:
  commands:
    - name: pi
      variables:
        - name: GEMINI_API_KEY
          key: gemini
          profile: prod
files:
  - path: ~/prod.json
    key: prod-file
    profile: prod
`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Vault.Addr, "http://default:8200"; got != want {
		t.Fatalf("default addr = %q, want %q", got, want)
	}
	if got, want := cfg.Vault.Profiles["prod"].KVVersion, 2; got != want {
		t.Fatalf("prod kv_version = %d, want %d", got, want)
	}
	if got, want := cfg.Vault.Profiles["prod"].EnvPrefix, "prod/env"; got != want {
		t.Fatalf("prod env_prefix = %q, want %q", got, want)
	}
	pi := cfg.FindCommand("pi")
	if pi == nil {
		t.Fatal("FindCommand(pi) = nil")
	}
	if got, want := pi.Variables[0].Profile, "prod"; got != want {
		t.Fatalf("variable profile = %q, want %q", got, want)
	}
	if got, want := cfg.Files[0].Profile, "prod"; got != want {
		t.Fatalf("file profile = %q, want %q", got, want)
	}
	if got, err := cfg.VaultProfile("default"); err != nil || got.Addr != "http://default:8200" {
		t.Fatalf("VaultProfile(default) = %#v, %v", got, err)
	}
	if _, err := cfg.VaultProfile("missing"); err == nil {
		t.Fatal("VaultProfile(missing) error = nil, want error")
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
