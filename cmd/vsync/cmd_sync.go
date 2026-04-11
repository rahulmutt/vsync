package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/config"
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

			cfgPath, err := resolveConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.LoadOrEmpty(cfgPath)
			if err != nil {
				return err
			}
			if err := cfg.ExpandPaths(); err != nil {
				return err
			}

			creds, err := vlt.LoadCredentials(dirs, key, resolveVaultAddr(), resolveVaultToken())
			if err != nil {
				return err
			}
			client, err := vlt.NewClient(creds, cfg.Vault.KVVersion)
			if err != nil {
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

			synced, skipped := 0, 0
			for _, f := range entries {
				// Check cache unless --force.
				if !force {
					entry, _ := vlt.ReadCache(dirs, key, "files", f.Key)
					if entry != nil && !entry.IsExpired() {
						fmt.Printf("  skipped (cached): %s\n", f.Key)
						skipped++
						continue
					}
				}
				content, err := vlt.GetCachedFileSecret(dirs, key, client, cfg.Vault.FilesPrefix, f.Key)
				if err != nil {
					fmt.Fprintf(os.Stderr, "vsync: error fetching %s: %v\n", f.Key, err)
					continue
				}
				if err := writeFile(f.Path, f.Mode, []byte(content)); err != nil {
					fmt.Fprintf(os.Stderr, "vsync: error writing %s: %v\n", f.Path, err)
					continue
				}
				fmt.Printf("  synced: %s → %s\n", f.Key, f.Path)
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
func syncFiles(dirs interface{ CacheFile(string, string) string }, key []byte, client *vlt.Client, cfg *config.Config) {
	// Re-use the logic but via the vault package directly.
	for _, f := range cfg.Files {
		content, err := vlt.GetCachedFileSecret(
			globalDirs, key, client, cfg.Vault.FilesPrefix, f.Key,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vsync: file sync %s: %v\n", f.Key, err)
			continue
		}
		if err := writeFile(f.Path, f.Mode, []byte(content)); err != nil {
			fmt.Fprintf(os.Stderr, "vsync: write %s: %v\n", f.Path, err)
			continue
		}
		fmt.Printf("vsync: synced %s → %s\n", f.Key, f.Path)
	}
}
