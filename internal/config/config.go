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

// VaultProfileConfig holds the settings for a single vault profile.
type VaultProfileConfig struct {
	Addr        string `yaml:"addr"`
	Token       string `yaml:"token"`
	EnvPrefix   string `yaml:"env_prefix"`
	FilesPrefix string `yaml:"files_prefix"`
	KVVersion   int    `yaml:"kv_version"`
}

// VaultConfig holds the default vault profile plus optional named profiles.
type VaultConfig struct {
	VaultProfileConfig `yaml:",inline"`
	Profiles           map[string]VaultProfileConfig `yaml:"profiles"`
}

// EnvConfig holds per-command environment variable injection config.
type EnvConfig struct {
	Commands []CommandEntry `yaml:"commands"`
}

// CommandEntry maps a command name to an optional CEL filter and vault-sourced variables.
type CommandEntry struct {
	Name      string          `yaml:"name"`
	Filter    string          `yaml:"filter"` // CEL expression over command args; inject only when true
	Variables []VariableEntry `yaml:"variables"`
}

// VariableEntry maps an env var name to a vault key.
// If Key is omitted, it defaults to Name.
type VariableEntry struct {
	Name    string `yaml:"name"`    // env var name, e.g. GEMINI_API_KEY
	Key     string `yaml:"key"`     // vault key, e.g. gemini-api-key
	Profile string `yaml:"profile"` // vault profile to use (default if omitted)
}

// FileEntry maps a local path to a vault key.
type FileEntry struct {
	Path    string `yaml:"path"`    // local file path (~ expanded)
	Key     string `yaml:"key"`     // vault key, e.g. pi-agent-auth
	Mode    string `yaml:"mode"`    // file permission, default "0600"
	Profile string `yaml:"profile"` // vault profile to use (default if omitted)
}

// defaults fills in zero values with sensible defaults.
func (c *Config) defaults() {
	c.Vault.defaults()
	for i := range c.Env.Commands {
		for j := range c.Env.Commands[i].Variables {
			if c.Env.Commands[i].Variables[j].Key == "" {
				c.Env.Commands[i].Variables[j].Key = c.Env.Commands[i].Variables[j].Name
			}
		}
	}
	for i := range c.Files {
		if c.Files[i].Mode == "" {
			c.Files[i].Mode = "0600"
		}
	}
}

func (v *VaultConfig) defaults() {
	v.VaultProfileConfig.defaults()
	for name, prof := range v.Profiles {
		prof.defaults()
		v.Profiles[name] = prof
	}
}

func (v *VaultProfileConfig) defaults() {
	if v.EnvPrefix == "" {
		v.EnvPrefix = "secret/data/vsync/env"
	}
	if v.FilesPrefix == "" {
		v.FilesPrefix = "secret/data/vsync/files"
	}
	if v.KVVersion == 0 {
		v.KVVersion = 2
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

// VaultProfile returns the named profile or the default profile if name is empty
// or "default".
func (c *Config) VaultProfile(name string) (VaultProfileConfig, error) {
	if name == "" || name == "default" {
		return c.Vault.VaultProfileConfig, nil
	}
	prof, ok := c.Vault.Profiles[name]
	if !ok {
		return VaultProfileConfig{}, fmt.Errorf("vault profile %q not found", name)
	}
	return prof, nil
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
	dst.Vault.merge(src.Vault)
	dst.Env.Commands = mergeCommands(dst.Env.Commands, src.Env.Commands)
	dst.Files = mergeFiles(dst.Files, src.Files)
}

func (dst *VaultConfig) merge(src VaultConfig) {
	dst.VaultProfileConfig = mergeVaultProfile(dst.VaultProfileConfig, src.VaultProfileConfig)
	dst.Profiles = mergeProfiles(dst.Profiles, src.Profiles)
}

func mergeProfiles(base map[string]VaultProfileConfig, overlay map[string]VaultProfileConfig) map[string]VaultProfileConfig {
	if base == nil {
		base = make(map[string]VaultProfileConfig, len(overlay))
	}
	for name, prof := range overlay {
		if existing, ok := base[name]; ok {
			base[name] = mergeVaultProfile(existing, prof)
			continue
		}
		base[name] = prof
	}
	return base
}

func mergeVaultProfile(base, overlay VaultProfileConfig) VaultProfileConfig {
	if overlay.Addr != "" {
		base.Addr = overlay.Addr
	}
	if overlay.Token != "" {
		base.Token = overlay.Token
	}
	if overlay.EnvPrefix != "" {
		base.EnvPrefix = overlay.EnvPrefix
	}
	if overlay.FilesPrefix != "" {
		base.FilesPrefix = overlay.FilesPrefix
	}
	if overlay.KVVersion != 0 {
		base.KVVersion = overlay.KVVersion
	}
	return base
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
	if overlay.Filter != "" {
		base.Filter = overlay.Filter
	}
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
