// Package config loads and validates ~/.config/vsync/config.yaml.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

var userHomeDirFn = os.UserHomeDir

// DefaultConfigPath returns the default config file path.
func DefaultConfigPath() (string, error) {
	home, err := userHomeDirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "vsync", "config.yaml"), nil
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.defaults()
	return &cfg, nil
}

// LoadOrEmpty loads the config if the file exists; returns a default config otherwise.
func LoadOrEmpty(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := &Config{}
		cfg.defaults()
		return cfg, nil
	}
	return Load(path)
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

func expandHome(path, home string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		return filepath.Join(home, path[2:])
	}
	if path == "~" {
		return home
	}
	return path
}
