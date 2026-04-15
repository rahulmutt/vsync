package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/shim"
	vlt "github.com/vsync/vsync/internal/vault"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show vsync state and configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs := globalDirs
			key := globalKey

			fmt.Println("=== vsync status ===")

			// Paths.
			keyPath := dirs.KeyFile()
			if flagKeyPath != "" {
				keyPath = flagKeyPath
			}
			printField("Key file", keyPath)
			printField("Cache dir", dirs.Cache)

			// Config.
			globalCfgPath, _ := resolveGlobalConfigPath()
			overrideCfgPath, _ := resolveConfigPath()
			printField("Global config", globalCfgPath)
			if overrideCfgPath == "" {
				printField("Override config", "search vsync.yaml in cwd/parents")
			} else {
				printField("Override config", overrideCfgPath)
			}
			cfg, cfgErr := loadConfigFn(globalCfgPath, overrideCfgPath)
			if cfgErr != nil {
				fmt.Printf("  %-20s %v\n", "Config error:", cfgErr)
			} else {
				// Vault address / token TTL for the default profile.
				defaultCreds, err := resolveVaultCredentialsForProfile(cfg, dirs, key, "default")
				if err != nil {
					fmt.Printf("  %-20s %s\n", "Credentials:", "NOT FOUND (run 'vsync init')")
				} else {
					truncAddr := defaultCreds.Addr
					if len(truncAddr) > 40 {
						truncAddr = truncAddr[:40] + "…"
					}
					printField("Vault address", truncAddr)

					// Token TTL.
					client, cerr := newVaultClientFn(defaultCreds, cfg.Vault.KVVersion)
					if cerr == nil {
						ttl, terr := client.TokenTTL()
						if terr == nil {
							if ttl == 0 {
								printField("Token TTL", "unlimited")
							} else {
								printField("Token TTL", ttl.Round(time.Second).String())
							}
						} else {
							printField("Token TTL", fmt.Sprintf("error: %v", terr))
						}
					}
				}

				fmt.Printf("\nVault profiles:\n")
				defaultProfile, err := vaultProfileLookupFn(cfg, "default")
				if err != nil {
					return err
				}
				fmt.Printf("  %-20s %s\n", "default", profileSummary(defaultProfile))
				for name := range cfg.Vault.Profiles {
					prof, err := vaultProfileLookupFn(cfg, name)
					if err != nil {
						return err
					}
					fmt.Printf("  %-20s %s\n", name, profileSummary(prof))
				}

				fmt.Printf("\nConfigured commands (%d):\n", len(cfg.Env.Commands))
				shimNames, _ := shim.List(dirs)
				shimSet := toSet(shimNames)
				for _, c := range cfg.Env.Commands {
					shimStatus := "  (shim missing)"
					if shimSet[c.Name] {
						shimStatus = "  (shim present)"
					}
					fmt.Printf("  %-20s %s\n", c.Name, shimStatus)
					if c.Filter != "" {
						fmt.Printf("    filter = %s\n", c.Filter)
					}
					for _, v := range c.Variables {
						profileName := v.Profile
						if profileName == "" {
							profileName = "default"
						}
						cacheEntry, _ := vlt.ReadCache(dirs, key, "env", profileName, v.Key)
						cacheStatus := "not cached"
						if cacheEntry != nil {
							if cacheEntry.IsExpired() {
								cacheStatus = "cached (expired)"
							} else if cacheEntry.ExpiresAt.IsZero() {
								cacheStatus = "cached (no expiry)"
							} else {
								remaining := time.Until(cacheEntry.ExpiresAt).Round(time.Second)
								cacheStatus = fmt.Sprintf("cached (expires in %s)", remaining)
							}
						}
						fmt.Printf("    %s = %s [%s, profile=%s]\n", v.Name, v.Key, cacheStatus, profileName)
					}
				}

				fmt.Printf("\nFile sync entries (%d):\n", len(cfg.Files))
				for _, f := range cfg.Files {
					profileName := f.Profile
					if profileName == "" {
						profileName = "default"
					}
					cacheEntry, _ := vlt.ReadCache(dirs, key, "files", profileName, f.Key)
					cacheStatus := "not cached"
					if cacheEntry != nil {
						if cacheEntry.IsExpired() {
							cacheStatus = "cached (expired)"
						} else {
							cacheStatus = "cached"
						}
					}
					_, statErr := os.Stat(f.Path)
					fileStatus := "exists"
					if errors.Is(statErr, os.ErrNotExist) {
						fileStatus = "missing"
					}
					fmt.Printf("  %-30s → %-40s [%s, %s, profile=%s]\n", f.Key, f.Path, fileStatus, cacheStatus, profileName)
				}
			}

			return nil
		},
	}
}

func printField(label, value string) {
	fmt.Printf("  %-20s %s\n", label+":", value)
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

func profileSummary(prof config.VaultProfileConfig) string {
	addr := prof.Addr
	if len(addr) > 40 {
		addr = addr[:40] + "…"
	}
	if addr == "" {
		addr = "(credentials not configured)"
	}
	return fmt.Sprintf("addr=%s env=%s files=%s kv=%d", addr, prof.EnvPrefix, prof.FilesPrefix, prof.KVVersion)
}
