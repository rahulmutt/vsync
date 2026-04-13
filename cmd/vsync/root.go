package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

var (
	flagVaultAddr        string
	flagVaultToken       string
	flagVaultEnvPrefix   string
	flagVaultFilesPrefix string
	flagVaultKVVersion   string
	flagGlobalConfigPath string
	flagConfigPath       string
	flagKeyPath          string
)

const defaultVaultProfileName = "default"

var userHomeDirFn = os.UserHomeDir
var exitFn = os.Exit
var resolveDirsFn = state.DefaultDirs

// globalKey is lazily loaded once per process.
var globalKey []byte
var globalDirs *state.Dirs

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "vsync",
		Short:   "Vault-synced shell environment",
		Version: fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		Long: `vsync creates an encrypted local cache of Vault credentials and launches a shell
where configured commands are shimmed to automatically inject secrets from Vault.

State lives under VSYNC_STATE_DIR / XDG_STATE_HOME / ~/.local/state/vsync, while the
secret cache lives under VSYNC_CACHE_DIR / XDG_CACHE_HOME / ~/.cache/vsync. Config is
loaded from a global config file and, optionally, a local override config.`,
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip for init subcommand (bootstraps dirs itself).
			if cmd.Name() == "init" {
				return nil
			}
			dirs, err := resolveDirsFn()
			if err != nil {
				return err
			}
			globalDirs = dirs
			key, err := resolveKey(dirs)
			if err != nil {
				return err
			}
			globalKey = key
			return nil
		},
	}

	root.PersistentFlags().StringVar(&flagVaultAddr, "vault-addr", "", "Vault server address (overrides VAULT_ADDR)")
	root.PersistentFlags().StringVar(&flagVaultToken, "vault-token", "", "Vault token (overrides VAULT_TOKEN)")
	root.PersistentFlags().StringVar(&flagVaultEnvPrefix, "vault-env-prefix", "", "Vault prefix for env secrets (overrides VSYNC_VAULT_ENV_PREFIX and config.yaml vault.env_prefix)")
	root.PersistentFlags().StringVar(&flagVaultFilesPrefix, "vault-files-prefix", "", "Vault prefix for file secrets (overrides VSYNC_VAULT_FILES_PREFIX and config.yaml vault.files_prefix)")
	root.PersistentFlags().StringVar(&flagVaultKVVersion, "vault-kv-version", "", "Vault KV version, 1 or 2 (overrides VSYNC_VAULT_KV_VERSION and config.yaml vault.kv_version)")
	root.PersistentFlags().StringVar(&flagGlobalConfigPath, "global-config", "", "Global config file path (default: $XDG_CONFIG_HOME/vsync/config.yaml or ~/.config/vsync/config.yaml; overrides VSYNC_GLOBAL_CONFIG)")
	root.PersistentFlags().StringVar(&flagConfigPath, "config", "", "Local override config file path (default: search for vsync.yaml in cwd/parents; overrides VSYNC_CONFIG)")
	root.PersistentFlags().StringVar(&flagKeyPath, "key", "", "Encryption key file path (default: ~/.local/state/vsync/keys/default.key)")

	root.AddCommand(
		initCmd(),
		shellCmd(),
		execCmd(),
		syncCmd(),
		statusCmd(),
		cacheCmd(),
	)

	return root
}

func resolveDirs() (*state.Dirs, error) {
	return state.DefaultDirs()
}

func resolveKey(dirs *state.Dirs) ([]byte, error) {
	keyPath := flagKeyPath
	if keyPath == "" {
		keyPath = os.Getenv("VSYNC_KEY")
	}
	if keyPath == "" {
		keyPath = dirs.KeyFile()
	}
	return crypto.LoadKey(keyPath)
}

func resolveGlobalConfigPath() (string, error) {
	if flagGlobalConfigPath != "" {
		return flagGlobalConfigPath, nil
	}
	if v := os.Getenv("VSYNC_GLOBAL_CONFIG"); v != "" {
		return v, nil
	}
	return defaultGlobalConfigPath()
}

func defaultGlobalConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg + "/vsync/config.yaml", nil
	}
	home, err := userHomeDirFn()
	if err != nil {
		return "", err
	}
	return home + "/.config/vsync/config.yaml", nil
}

func resolveConfigPath() (string, error) {
	if flagConfigPath != "" {
		return flagConfigPath, nil
	}
	if v := os.Getenv("VSYNC_CONFIG"); v != "" {
		return v, nil
	}
	return "", nil
}

func resolveVaultAddr() string {
	if flagVaultAddr != "" {
		return flagVaultAddr
	}
	return os.Getenv("VAULT_ADDR")
}

func resolveVaultToken() string {
	if flagVaultToken != "" {
		return flagVaultToken
	}
	return os.Getenv("VAULT_TOKEN")
}

func resolveVaultEnvPrefix() string {
	if flagVaultEnvPrefix != "" {
		return flagVaultEnvPrefix
	}
	return os.Getenv("VSYNC_VAULT_ENV_PREFIX")
}

func resolveVaultFilesPrefix() string {
	if flagVaultFilesPrefix != "" {
		return flagVaultFilesPrefix
	}
	return os.Getenv("VSYNC_VAULT_FILES_PREFIX")
}

func resolveVaultKVVersion() (int, error) {
	raw := flagVaultKVVersion
	if raw == "" {
		raw = os.Getenv("VSYNC_VAULT_KV_VERSION")
	}
	if raw == "" {
		return 0, nil
	}
	version, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse VSYNC_VAULT_KV_VERSION %q: %w", raw, err)
	}
	if version != 1 && version != 2 {
		return 0, fmt.Errorf("invalid vault kv version %d: must be 1 or 2", version)
	}
	return version, nil
}

func applyVaultOverrides(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	if v := resolveVaultEnvPrefix(); v != "" {
		cfg.Vault.EnvPrefix = v
	}
	if v := resolveVaultFilesPrefix(); v != "" {
		cfg.Vault.FilesPrefix = v
	}
	if v, err := resolveVaultKVVersion(); err != nil {
		return err
	} else if v != 0 {
		cfg.Vault.KVVersion = v
	}
	return nil
}

func resolveVaultCredentialsForProfile(cfg *config.Config, dirs *state.Dirs, key []byte, profile string) (*vlt.Credentials, error) {
	prof, err := cfg.VaultProfile(profile)
	if err != nil {
		return nil, err
	}
	if profile == "" || profile == defaultVaultProfileName {
		addrOverride := resolveVaultAddr()
		if addrOverride == "" {
			addrOverride = prof.Addr
		}
		tokenOverride := resolveVaultToken()
		if tokenOverride == "" {
			tokenOverride = prof.Token
		}
		return loadCredsFn(dirs, key, addrOverride, tokenOverride)
	}
	if creds, err := loadProfileCredsFn(dirs, key, profile); err == nil {
		return creds, nil
	}
	if prof.Addr != "" && prof.Token != "" {
		return &vlt.Credentials{Addr: prof.Addr, Token: prof.Token}, nil
	}
	return nil, fmt.Errorf("vault credentials for profile %q not found; run 'vsync init' first", profile)
}

func loadConfigWithOverrides(globalPath, overridePath string) (*config.Config, error) {
	cfg, err := config.LoadOrEmpty(globalPath, overridePath)
	if err != nil {
		return nil, err
	}
	if err := applyVaultOverrides(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "vsync: "+format+"\n", a...)
	exitFn(1)
}
