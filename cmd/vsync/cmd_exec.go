package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/shell"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

var (
	loadVaultCredentials = vlt.LoadCredentials
	newVaultClient       = vlt.NewClient
	getCachedEnvSecret   = vlt.GetCachedEnvSecret
	execRealCommand      = shell.ExecCommand
)

func execCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "exec <command> [args...]",
		Short:              "Fetch secrets for a command and exec it (used by shims)",
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			commandName := args[0]
			commandArgs := args[1:]

			dirs := globalDirs
			key := globalKey

			globalCfgPath, err := resolveGlobalConfigPath()
			if err != nil {
				return err
			}
			overrideCfgPath, err := resolveConfigPath()
			if err != nil {
				return err
			}
			cfg, err := loadConfigFn(globalCfgPath, overrideCfgPath)
			if err != nil {
				return err
			}

			return execConfiguredCommand(cfg, commandName, commandArgs, dirs, key)
		},
	}
	return cmd
}

func execConfiguredCommand(cfg *config.Config, commandName string, commandArgs []string, dirs *state.Dirs, key []byte) error {
	entry := cfg.FindCommand(commandName)
	if entry == nil {
		// No config for this command — exec it directly without modifications.
		return execRealCommand(commandName, commandArgs, nil, dirs.Shims)
	}

	// Fetch secrets for each variable.
	creds, err := loadVaultCredentials(dirs, key, resolveVaultAddr(), resolveVaultToken())
	if err != nil {
		return fmt.Errorf("load vault credentials: %w", err)
	}
	client, err := newVaultClient(creds, cfg.Vault.KVVersion)
	if err != nil {
		return fmt.Errorf("vault client: %w", err)
	}

	extraEnv := make(map[string]string, len(entry.Variables))
	for _, v := range entry.Variables {
		value, err := getCachedEnvSecret(dirs, key, client, cfg.Vault.EnvPrefix, v.Key)
		if err != nil {
			return fmt.Errorf("fetch secret %s: %w", v.Key, err)
		}
		extraEnv[v.Name] = value
	}

	return execRealCommand(commandName, commandArgs, extraEnv, dirs.Shims)
}
