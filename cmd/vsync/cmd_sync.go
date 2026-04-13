package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

func syncCmd() *cobra.Command {
	var fileKey string
	var force bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync files from Vault to local paths",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			entries := cfg.Files
			if fileKey != "" {
				entries = nil
				for _, f := range cfg.Files {
					if f.Key == fileKey {
						entries = append(entries, f)
					}
				}
				if len(entries) == 0 {
					return fmt.Errorf("no file entry with key %q found in config", fileKey)
				}
			}

			credsByProfile := map[string]*vlt.Client{}
			synced, skipped := 0, 0
			for _, f := range entries {
				profileName := f.Profile
				if profileName == "" {
					profileName = "default"
				}
				profileCfg, err := cfg.VaultProfile(profileName)
				if err != nil {
					return err
				}
				client := credsByProfile[profileName]
				if client == nil {
					creds, err := resolveVaultCredentialsForProfile(cfg, dirs, key, profileName)
					if err != nil {
						return fmt.Errorf("load vault credentials for profile %q: %w", profileName, err)
					}
					client, err = newVaultClientFn(creds, profileCfg.KVVersion)
					if err != nil {
						return fmt.Errorf("vault client for profile %q: %w", profileName, err)
					}
					credsByProfile[profileName] = client
				}

				if !force {
					entry, _ := vlt.ReadCache(dirs, key, "files", profileName, f.Key)
					if entry != nil && !entry.IsExpired() {
						fmt.Printf("  skipped (cached): %s (%s)\n", f.Key, profileName)
						skipped++
						continue
					}
				}

				var content string
				if force {
					result, err := client.GetFileSecret(profileCfg.FilesPrefix, f.Key)
					if err != nil {
						fmt.Fprintf(os.Stderr, "vsync: error fetching %s (%s): %v\n", f.Key, profileName, err)
						continue
					}
					content = result.Value
					_ = vlt.WriteCache(dirs, key, "files", profileName, f.Key, &vlt.CacheEntry{Value: content, VaultPath: profileCfg.FilesPrefix + "/" + f.Key})
				} else {
					var err error
					content, err = vlt.GetCachedFileSecret(dirs, key, client, profileCfg.FilesPrefix, f.Key, profileName)
					if err != nil {
						fmt.Fprintf(os.Stderr, "vsync: error fetching %s (%s): %v\n", f.Key, profileName, err)
						continue
					}
				}

				if err := writeFile(f.Path, f.Mode, []byte(content)); err != nil {
					fmt.Fprintf(os.Stderr, "vsync: error writing %s: %v\n", f.Path, err)
					continue
				}
				fmt.Printf("  synced: %s → %s (%s)\n", f.Key, f.Path, profileName)
				synced++
			}
			fmt.Printf("vsync sync: %d synced, %d skipped\n", synced, skipped)
			return nil
		},
	}

	cmd.Flags().StringVar(&fileKey, "file", "", "Sync only the entry with this vault key")
	cmd.Flags().BoolVar(&force, "force", false, "Force re-fetch even if cache is fresh")
	return cmd
}

func writeFile(path, modeStr string, content []byte) error {
	if modeStr == "" {
		modeStr = "0600"
	}
	mode64, err := strconv.ParseUint(modeStr, 8, 32)
	if err != nil {
		return fmt.Errorf("invalid mode %q: %w", modeStr, err)
	}
	mode := os.FileMode(mode64)

	dir := ""
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			dir = path[:i]
			break
		}
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(content)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// syncFiles is called from cmd_shell.go to sync files during shell launch.
func syncFiles(dirs *state.Dirs, key []byte, client *vlt.Client, cfg *config.Config) {
	clients := map[string]*vlt.Client{}
	if client != nil {
		clients["default"] = client
	}
	for _, f := range cfg.Files {
		profileName := f.Profile
		if profileName == "" {
			profileName = "default"
		}
		profileCfg, err := cfg.VaultProfile(profileName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vsync: file sync %s (%s): %v\n", f.Key, profileName, err)
			continue
		}
		client := clients[profileName]
		if client == nil {
			creds, err := resolveVaultCredentialsForProfile(cfg, dirs, key, profileName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "vsync: file sync %s (%s): %v\n", f.Key, profileName, err)
				continue
			}
			client, err = newVaultClientFn(creds, profileCfg.KVVersion)
			if err != nil {
				fmt.Fprintf(os.Stderr, "vsync: file sync %s (%s): %v\n", f.Key, profileName, err)
				continue
			}
			clients[profileName] = client
		}
		content, err := vlt.GetCachedFileSecret(dirs, key, client, profileCfg.FilesPrefix, f.Key, profileName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vsync: file sync %s (%s): %v\n", f.Key, profileName, err)
			continue
		}
		if err := writeFile(f.Path, f.Mode, []byte(content)); err != nil {
			fmt.Fprintf(os.Stderr, "vsync: write %s: %v\n", f.Path, err)
			continue
		}
		fmt.Printf("vsync: synced %s → %s (%s)\n", f.Key, f.Path, profileName)
	}
}
