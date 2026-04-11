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

			// Key file.
			keyPath := dirs.KeyFile()
			if flagKeyPath != "" {
				keyPath = flagKeyPath
			}
			printField("Key file", keyPath)

			// Vault address.
			creds, err := vlt.LoadCredentials(dirs, key, resolveVaultAddr(), resolveVaultToken())
			if err != nil {
				fmt.Printf("  %-20s %s\n", "Credentials:", "NOT FOUND (run 'vsync init')")
			} else {
				truncAddr := creds.Addr
				if len(truncAddr) > 40 {
					truncAddr = truncAddr[:40] + "…"
				}
				printField("Vault address", truncAddr)

				// Token TTL.
				client, cerr := vlt.NewClient(creds, 2)
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

			// Config.
			cfgPath, _ := resolveConfigPath()
			printField("Config file", cfgPath)
			cfg, cfgErr := config.LoadOrEmpty(cfgPath)
			if cfgErr != nil {
				fmt.Printf("  %-20s %v\n", "Config error:", cfgErr)
			} else {
				fmt.Printf("\nConfigured commands (%d):\n", len(cfg.Env.Commands))
				shimNames, _ := shim.List(dirs)
				shimSet := toSet(shimNames)
				for _, c := range cfg.Env.Commands {
					shimStatus := "  (shim missing)"
					if shimSet[c.Name] {
						shimStatus = "  (shim present)"
					}
					fmt.Printf("  %-20s %s\n", c.Name, shimStatus)
					for _, v := range c.Variables {
						cacheEntry, _ := vlt.ReadCache(dirs, key, "env", v.Key)
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
						fmt.Printf("    %s = %s [%s]\n", v.Name, v.Key, cacheStatus)
					}
				}

				fmt.Printf("\nFile sync entries (%d):\n", len(cfg.Files))
				for _, f := range cfg.Files {
					cacheEntry, _ := vlt.ReadCache(dirs, key, "files", f.Key)
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
					fmt.Printf("  %-30s → %-40s [%s, %s]\n", f.Key, f.Path, fileStatus, cacheStatus)
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
