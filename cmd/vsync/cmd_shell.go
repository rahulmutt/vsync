package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/shell"
	"github.com/vsync/vsync/internal/shim"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

var (
	shellLaunchFn = shell.Launch
	loadConfigFn  = loadConfigWithOverrides
	expandPathsFn = func(cfg *config.Config) error { return cfg.ExpandPaths() }
	ensureShimsFn = shim.Ensure
	loadCredsFn   = func(dirs *state.Dirs, key []byte, addrOverride, tokenOverride string) (*vlt.Credentials, error) {
		return loadVaultCredentials(dirs, key, addrOverride, tokenOverride)
	}
	loadProfileCredsFn = func(dirs *state.Dirs, key []byte, profile string) (*vlt.Credentials, error) {
		return vlt.LoadCredentialsForProfile(dirs, key, profile, "", "")
	}
	newVaultClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
		return newVaultClient(creds, kvVersion)
	}
	syncFilesFn = syncFiles
)

func shellCmd() *cobra.Command {
	var shellBin string

	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Launch a new shell with vault-injected environment",
		Long: `shell syncs configured files from Vault, generates command shims, then
exec's into a new shell with the shims directory prepended to PATH.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("VSYNC_ACTIVE") == "1" {
				return fmt.Errorf("already inside a vsync shell; nested shells are not supported")
			}

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
			if err := expandPathsFn(cfg); err != nil {
				return err
			}

			// Collect command names from config.
			var commandNames []string
			for _, c := range cfg.Env.Commands {
				commandNames = append(commandNames, c.Name)
			}

			// Ensure shim scripts exist.
			if err := ensureShimsFn(dirs, commandNames); err != nil {
				return fmt.Errorf("build shims: %w", err)
			}

			// Sync files from Vault (best-effort; warn on failure).
			if len(cfg.Files) > 0 {
				syncFilesFn(dirs, key, nil, cfg)
			}

			// Resolve shell binary.
			if shellBin == "" {
				shellBin = os.Getenv("SHELL")
			}
			if shellBin == "" {
				shellBin = "/bin/sh"
			}

			keyPath := dirs.KeyFile()
			if flagKeyPath != "" {
				keyPath = flagKeyPath
			}

			fmt.Printf("vsync: launching %s with %d shim(s)\n", shellBin, len(commandNames))
			return shellLaunchFn(shellBin, dirs.Shims, keyPath)
		},
	}

	cmd.Flags().StringVar(&shellBin, "shell", "", "Shell binary to launch (default: $SHELL or /bin/sh)")
	return cmd
}
