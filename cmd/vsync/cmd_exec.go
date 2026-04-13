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
			overrideCfgPath, _ := resolveConfigPath()
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
	extraEnv := make(map[string]string, len(entry.Variables))
	clients := map[string]*vlt.Client{}
	for _, v := range entry.Variables {
		profileName := v.Profile
		if profileName == "" {
			profileName = "default"
		}
		profileCfg, err := cfg.VaultProfile(profileName)
		if err != nil {
			return err
		}
		client := clients[profileName]
		if client == nil {
			creds, err := resolveVaultCredentialsForProfile(cfg, dirs, key, profileName)
			if err != nil {
				return fmt.Errorf("load vault credentials for profile %q: %w", profileName, err)
			}
			client, err = newVaultClientFn(creds, profileCfg.KVVersion)
			if err != nil {
				return fmt.Errorf("vault client for profile %q: %w", profileName, err)
			}
			clients[profileName] = client
		}
		value, err := getCachedEnvSecret(dirs, key, client, profileCfg.EnvPrefix, v.Key, profileName)
		if err != nil {
			return fmt.Errorf("fetch secret %s (%s): %w", v.Key, profileName, err)
		}
		extraEnv[v.Name] = value
	}

	return execRealCommand(commandName, commandArgs, extraEnv, dirs.Shims)
}
