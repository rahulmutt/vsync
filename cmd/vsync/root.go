package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
)

var (
	flagVaultAddr  string
	flagVaultToken string
	flagConfigPath string
	flagKeyPath    string
)

// globalKey is lazily loaded once per process.
var globalKey []byte
var globalDirs *state.Dirs

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "vsync",
		Short:   "Vault-synced shell environment",
		Version: fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		Long: `vsync creates an encrypted local cache of Vault credentials and launches a shell
where configured commands are shimmed to automatically inject secrets from Vault.`,
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip for init subcommand (bootstraps dirs itself).
			if cmd.Name() == "init" {
				return nil
			}
			dirs, err := resolveDirs()
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
	root.PersistentFlags().StringVar(&flagConfigPath, "config", "", "Config file path (default: ~/.config/vsync/config.yaml)")
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

func resolveConfigPath() (string, error) {
	if flagConfigPath != "" {
		return flagConfigPath, nil
	}
	if v := os.Getenv("VSYNC_CONFIG"); v != "" {
		return v, nil
	}
	return defaultConfigPath()
}

func defaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + "/.config/vsync/config.yaml", nil
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

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "vsync: "+format+"\n", a...)
	os.Exit(1)
}
