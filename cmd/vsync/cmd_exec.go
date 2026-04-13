package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/celfilter"
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
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "exec <command> [args...]",
		Short: "Fetch secrets for a command and exec it (used by shims)",
		Long: `exec resolves the configured command, evaluates its filter, fetches any
required Vault-backed environment variables, and execs the real binary.

Use --dry-run to inspect whether the current invocation matches the configured
filter and which environment variables would be injected.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			commandName := args[0]
			commandArgs := args[1:]

			globalCfgPath, err := resolveGlobalConfigPath()
			if err != nil {
				return err
			}
			overrideCfgPath, _ := resolveConfigPath()
			cfg, err := loadConfigFn(globalCfgPath, overrideCfgPath)
			if err != nil {
				return err
			}

			if dryRun {
				return dryRunConfiguredCommand(cmd.OutOrStdout(), cfg, commandName, commandArgs)
			}

			dirs := globalDirs
			key := globalKey
			return execConfiguredCommand(cfg, commandName, commandArgs, dirs, key)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show whether the invocation matches the configured filter and which env vars would be injected")
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func execConfiguredCommand(cfg *config.Config, commandName string, commandArgs []string, dirs *state.Dirs, key []byte) error {
	entry, matched, err := resolveExecCommand(cfg, commandName, commandArgs)
	if err != nil {
		return err
	}
	if entry == nil || !matched {
		// No config for this command, or filter did not match — exec it directly.
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

func dryRunConfiguredCommand(out io.Writer, cfg *config.Config, commandName string, commandArgs []string) error {
	entry, matched, err := resolveExecCommand(cfg, commandName, commandArgs)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "vsync: dry-run for %q\n", commandName)
	if entry == nil {
		fmt.Fprintln(out, "vsync: command is not configured; no environment variables would be injected")
		return nil
	}

	if entry.Filter == "" {
		fmt.Fprintln(out, "vsync: filter matched: true (no filter configured)")
	} else {
		fmt.Fprintf(out, "vsync: filter matched: %t\n", matched)
	}

	if !matched || len(entry.Variables) == 0 {
		fmt.Fprintln(out, "vsync: environment variables to inject: none")
		return nil
	}

	fmt.Fprintln(out, "vsync: environment variables to inject:")
	for _, v := range entry.Variables {
		fmt.Fprintf(out, "vsync:   %s\n", v.Name)
	}
	return nil
}

func resolveExecCommand(cfg *config.Config, commandName string, commandArgs []string) (*config.CommandEntry, bool, error) {
	entry := cfg.FindCommand(commandName)
	if entry == nil {
		return nil, false, nil
	}

	if entry.Filter == "" {
		return entry, true, nil
	}
	matched, err := celfilter.Matches(entry.Filter, commandArgs)
	if err != nil {
		return nil, false, fmt.Errorf("evaluate filter for command %q: %w", commandName, err)
	}
	return entry, matched, nil
}
