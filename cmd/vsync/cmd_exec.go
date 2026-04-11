package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/shell"
	vlt "github.com/vsync/vsync/internal/vault"
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

			cfgPath, err := resolveConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.LoadOrEmpty(cfgPath)
			if err != nil {
				return err
			}

			entry := cfg.FindCommand(commandName)
			if entry == nil {
				// No config for this command — exec it directly without modifications.
				return shell.ExecCommand(commandName, commandArgs, nil, dirs.Shims)
			}

			// Fetch secrets for each variable.
			creds, err := vlt.LoadCredentials(dirs, key, resolveVaultAddr(), resolveVaultToken())
			if err != nil {
				return fmt.Errorf("load vault credentials: %w", err)
			}
			client, err := vlt.NewClient(creds, cfg.Vault.KVVersion)
			if err != nil {
				return fmt.Errorf("vault client: %w", err)
			}

			extraEnv := make(map[string]string, len(entry.Variables))
			for _, v := range entry.Variables {
				value, err := vlt.GetCachedEnvSecret(dirs, key, client, cfg.Vault.EnvPrefix, v.Key)
				if err != nil {
					return fmt.Errorf("fetch secret %s: %w", v.Key, err)
				}
				extraEnv[v.Name] = value
			}

			return shell.ExecCommand(commandName, commandArgs, extraEnv, dirs.Shims)
		},
	}
	return cmd
}
