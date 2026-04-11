package main

import (
	"fmt"

	"github.com/spf13/cobra"
	vlt "github.com/vsync/vsync/internal/vault"
)

func cacheCmd() *cobra.Command {
	cache := &cobra.Command{
		Use:   "cache",
		Short: "Manage the local secret cache",
	}

	var clearAll, clearEnv, clearFiles bool
	var clearKey string

	clearCmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear cached secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs := globalDirs

			if clearKey != "" {
				kind := "env"
				if clearFiles {
					kind = "files"
				}
				if err := vlt.DeleteCache(dirs, kind, clearKey); err != nil {
					return err
				}
				fmt.Printf("vsync: cleared cache entry %s/%s\n", kind, clearKey)
				return nil
			}

			if clearAll || clearEnv {
				if err := vlt.ClearCacheKind(dirs, "env"); err != nil {
					return err
				}
				fmt.Println("vsync: cleared env secret cache")
			}
			if clearAll || clearFiles {
				if err := vlt.ClearCacheKind(dirs, "files"); err != nil {
					return err
				}
				fmt.Println("vsync: cleared files secret cache")
			}
			if !clearAll && !clearEnv && !clearFiles && clearKey == "" {
				return fmt.Errorf("specify --all, --env, --files, or --key <name>")
			}
			return nil
		},
	}

	clearCmd.Flags().BoolVar(&clearAll, "all", false, "Clear all cached secrets")
	clearCmd.Flags().BoolVar(&clearEnv, "env", false, "Clear env-variable secret cache")
	clearCmd.Flags().BoolVar(&clearFiles, "files", false, "Clear file secret cache")
	clearCmd.Flags().StringVar(&clearKey, "key", "", "Clear a single cache entry by key name")

	cache.AddCommand(clearCmd)
	return cache
}
