// Package config loads and merges vsync config files.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

var userHomeDirFn = os.UserHomeDir
var workingDirFn = os.Getwd
var absPathFn = filepath.Abs
var statFn = os.Stat

// DefaultGlobalConfigPath returns the default global config file path.
func DefaultGlobalConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "vsync", "config.yaml"), nil
	}
	home, err := userHomeDirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "vsync", "config.yaml"), nil
}

// DefaultConfigPath is kept for backwards compatibility.
func DefaultConfigPath() (string, error) {
	return DefaultGlobalConfigPath()
}

// Config is the top-level vsync configuration.
type Config struct {
	Vault VaultConfig `yaml:"vault"`
	Env   EnvConfig   `yaml:"env"`
	Files []FileEntry `yaml:"files"`
}

// VaultConfig holds vault-specific settings.
type VaultConfig struct {
	EnvPrefix   string `yaml:"env_prefix"`
	FilesPrefix string `yaml:"files_prefix"`
	KVVersion   int    `yaml:"kv_version"`
}

// EnvConfig holds per-command environment variable injection config.
type EnvConfig struct {
	Commands []CommandEntry `yaml:"commands"`
}

// CommandEntry maps a command name to vault-sourced variables.
type CommandEntry struct {
	Name      string          `yaml:"name"`
	Variables []VariableEntry `yaml:"variables"`
}

// VariableEntry maps an env var name to a vault key.
type VariableEntry struct {
	Name string `yaml:"name"` // env var name, e.g. GEMINI_API_KEY
	Key  string `yaml:"key"`  // vault key, e.g. gemini-api-key
}

// FileEntry maps a local path to a vault key.
type FileEntry struct {
	Path string `yaml:"path"` // local file path (~ expanded)
	Key  string `yaml:"key"`  // vault key, e.g. pi-agent-auth
	Mode string `yaml:"mode"` // file permission, default "0600"
}

// defaults fills in zero values with sensible defaults.
func (c *Config) defaults() {
	if c.Vault.EnvPrefix == "" {
		c.Vault.EnvPrefix = "secret/data/vsync/env"
	}
	if c.Vault.FilesPrefix == "" {
		c.Vault.FilesPrefix = "secret/data/vsync/files"
	}
	if c.Vault.KVVersion == 0 {
		c.Vault.KVVersion = 2
	}
	for i := range c.Files {
		if c.Files[i].Mode == "" {
			c.Files[i].Mode = "0600"
		}
	}
}

// Load reads and parses the config file at path.
func Load(path string) (*Config, error) {
	cfg, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	cfg.defaults()
	return cfg, nil
}

// LoadOrEmpty loads the global config file plus any local override config,
// merging them from top to bottom. If overridePath is empty, it searches for
// vsync.yaml in the current directory and its parents. Missing files are ignored.
func LoadOrEmpty(globalPath, overridePath string) (*Config, error) {
	paths, err := configPaths(globalPath, overridePath)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	for _, p := range paths {
		src, err := loadFile(p)
		if err != nil {
			return nil, err
		}
		mergeConfig(cfg, src)
	}
	cfg.defaults()
	return cfg, nil
}

// FindCommand returns the CommandEntry for the given command name, or nil.
func (c *Config) FindCommand(name string) *CommandEntry {
	for i := range c.Env.Commands {
		if c.Env.Commands[i].Name == name {
			return &c.Env.Commands[i]
		}
	}
	return nil
}

// ExpandPaths expands ~ in all file paths.
func (c *Config) ExpandPaths() error {
	home, err := userHomeDirFn()
	if err != nil {
		return err
	}
	for i := range c.Files {
		c.Files[i].Path = expandHome(c.Files[i].Path, home)
	}
	return nil
}

func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}

func configPaths(globalPath, overridePath string) ([]string, error) {
	paths := []string{}
	seen := map[string]struct{}{}
	add := func(path string) error {
		abs, err := absPathFn(path)
		if err != nil {
			abs = filepath.Clean(path)
		}
		if _, ok := seen[abs]; ok {
			return nil
		}
		if _, err := statFn(path); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		seen[abs] = struct{}{}
		paths = append(paths, path)
		return nil
	}

	if globalPath == "" {
		var err error
		globalPath, err = DefaultGlobalConfigPath()
		if err != nil {
			return nil, err
		}
	}
	if err := add(globalPath); err != nil {
		return nil, err
	}

	if overridePath != "" {
		if err := add(overridePath); err != nil {
			return nil, err
		}
		return paths, nil
	}

	cwd, err := workingDirFn()
	if err != nil {
		return nil, err
	}
	var localPaths []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		localPaths = append(localPaths, filepath.Join(dir, "vsync.yaml"))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	for i := len(localPaths) - 1; i >= 0; i-- {
		if err := add(localPaths[i]); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func mergeConfig(dst, src *Config) {
	if src == nil {
		return
	}
	if src.Vault.EnvPrefix != "" {
		dst.Vault.EnvPrefix = src.Vault.EnvPrefix
	}
	if src.Vault.FilesPrefix != "" {
		dst.Vault.FilesPrefix = src.Vault.FilesPrefix
	}
	if src.Vault.KVVersion != 0 {
		dst.Vault.KVVersion = src.Vault.KVVersion
	}
	dst.Env.Commands = mergeCommands(dst.Env.Commands, src.Env.Commands)
	dst.Files = mergeFiles(dst.Files, src.Files)
}

func mergeCommands(base []CommandEntry, overlay []CommandEntry) []CommandEntry {
	index := make(map[string]int, len(base))
	for i := range base {
		index[base[i].Name] = i
	}
	for _, cmd := range overlay {
		if i, ok := index[cmd.Name]; ok {
			base[i] = mergeCommand(base[i], cmd)
			continue
		}
		index[cmd.Name] = len(base)
		base = append(base, cmd)
	}
	return base
}

func mergeCommand(base, overlay CommandEntry) CommandEntry {
	base.Variables = mergeVariables(base.Variables, overlay.Variables)
	return base
}

func mergeVariables(base []VariableEntry, overlay []VariableEntry) []VariableEntry {
	index := make(map[string]int, len(base))
	for i := range base {
		index[base[i].Name] = i
	}
	for _, v := range overlay {
		if i, ok := index[v.Name]; ok {
			base[i] = v
			continue
		}
		index[v.Name] = len(base)
		base = append(base, v)
	}
	return base
}

func mergeFiles(base []FileEntry, overlay []FileEntry) []FileEntry {
	index := make(map[string]int, len(base))
	for i := range base {
		index[base[i].Key] = i
	}
	for _, f := range overlay {
		if i, ok := index[f.Key]; ok {
			base[i] = f
			continue
		}
		index[f.Key] = len(base)
		base = append(base, f)
	}
	return base
}

func expandHome(path, home string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		return filepath.Join(home, path[2:])
	}
	if path == "~" {
		return home
	}
	return path
}
