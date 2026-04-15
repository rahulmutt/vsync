package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
	"golang.org/x/term"
)

var (
	defaultDirsFn       = state.DefaultDirs
	generateKeyFn       = crypto.GenerateKey
	loadOrGenerateKeyFn = crypto.LoadOrGenerateKey
	loadKeyFn           = crypto.LoadKey
	storeCredentialsFn  = vlt.StoreCredentials
	storeProfileCredsFn = vlt.StoreCredentialsForProfile
	newClientFn         = vlt.NewClient
	resolveVaultAddrFn  = resolveVaultAddr
	resolveVaultTokenFn = resolveVaultToken
	promptFn            = prompt
	isTerminalFn        = term.IsTerminal
	readPasswordFn      = term.ReadPassword
)

func initCmd() *cobra.Command {
	var rotateKey bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Store Vault credentials encrypted on disk",
		Long: `init generates a local encryption key (if not present) and stores Vault
credentials for the default profile plus any additional configured profiles on disk
so vsync can connect to Vault without exposing credentials in plain text.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs, err := defaultDirsFn()
			if err != nil {
				return err
			}
			if err := dirs.EnsureAll(); err != nil {
				return err
			}

			globalCfgPath, err := resolveGlobalConfigPath()
			if err != nil {
				return err
			}
			overrideCfgPath, _ := resolveConfigPath()
			cfg, err := loadConfigFn(globalCfgPath, overrideCfgPath)
			if err != nil {
				return err
			}

			// Resolve key path.
			keyPath := flagKeyPath
			if keyPath == "" {
				keyPath = dirs.KeyFile()
			}

			// Generate or rotate key.
			if rotateKey {
				fmt.Println("vsync: rotating encryption key…")
				if _, err := generateKeyFn(keyPath); err != nil {
					return err
				}
			} else {
				if _, err := loadOrGenerateKeyFn(keyPath); err != nil {
					return err
				}
			}
			key, err := loadKeyFn(keyPath)
			if err != nil {
				return err
			}

			profiles := make(map[string]struct{}, len(cfg.Vault.Profiles)+1)
			profiles[defaultVaultProfileName] = struct{}{}
			for name := range cfg.Vault.Profiles {
				profiles[name] = struct{}{}
			}
			profileNames := make([]string, 0, len(profiles))
			for name := range profiles {
				profileNames = append(profileNames, name)
			}
			sort.Strings(profileNames)

			for _, profileName := range profileNames {
				prof, err := cfg.VaultProfile(profileName)
				if err != nil {
					return err
				}
				label := profileName
				if profileName == defaultVaultProfileName {
					label = "default"
				}

				addr := prof.Addr
				if profileName == defaultVaultProfileName {
					if v := resolveVaultAddrFn(); v != "" {
						addr = v
					}
				}
				if addr == "" {
					if profileName == defaultVaultProfileName {
						if stored, err := loadCredsFn(dirs, key, "", ""); err == nil {
							addr = stored.Addr
						}
					} else {
						if stored, err := loadProfileCredsFn(dirs, key, profileName); err == nil {
							addr = stored.Addr
						}
					}
				}
				if addr == "" {
					addr, err = promptFn(fmt.Sprintf("Vault address (%s): ", label), false)
					if err != nil {
						return err
					}
				}
				addr = strings.TrimSpace(addr)
				if addr == "" {
					return fmt.Errorf("vault address is required for profile %q", profileName)
				}

				token := prof.Token
				if profileName == defaultVaultProfileName {
					if v := resolveVaultTokenFn(); v != "" {
						token = v
					}
				}
				if token == "" {
					if profileName == defaultVaultProfileName {
						if stored, err := loadCredsFn(dirs, key, "", ""); err == nil {
							token = stored.Token
						}
					} else {
						if stored, err := loadProfileCredsFn(dirs, key, profileName); err == nil {
							token = stored.Token
						}
					}
				}
				if token == "" {
					token, err = promptFn(fmt.Sprintf("Vault token (%s): ", label), true)
					if err != nil {
						return err
					}
				}
				token = strings.TrimSpace(token)
				if token == "" {
					return fmt.Errorf("vault token is required for profile %q", profileName)
				}

				if profileName == defaultVaultProfileName {
					if err := storeCredentialsFn(dirs, key, addr, token); err != nil {
						return err
					}
				} else {
					if err := storeProfileCredsFn(dirs, key, profileName, addr, token); err != nil {
						return err
					}
				}

				fmt.Printf("vsync: verifying vault connectivity for profile %s… ", label)
				creds := &vlt.Credentials{Addr: addr, Token: token}
				client, err := newClientFn(creds, prof.KVVersion)
				if err != nil {
					fmt.Println("✗")
					return fmt.Errorf("create vault client for profile %q: %w", profileName, err)
				}
				if err := client.Ping(); err != nil {
					fmt.Println("✗")
					fmt.Fprintf(os.Stderr, "vsync: warning: vault ping failed for profile %s: %v\n", label, err)
					fmt.Printf("vsync: credentials stored for profile %s, but vault is not reachable right now.\n", label)
				} else {
					fmt.Println("✓")
					ttl, err := client.TokenTTL()
					if err == nil && ttl > 0 && ttl.Hours() < 1 {
						fmt.Fprintf(os.Stderr, "vsync: warning: vault token for profile %s expires in %.0f minutes\n", label, ttl.Minutes())
					}
				}
			}

			fmt.Printf("vsync: credentials stored at %s\n", dirs.Tokens)
			fmt.Printf("vsync: cache stored at       %s\n", dirs.Cache)
			fmt.Printf("vsync: encryption key at     %s\n", keyPath)
			return nil
		},
	}

	cmd.Flags().BoolVar(&rotateKey, "rotate-key", false, "Generate a new encryption key (re-encrypt existing tokens)")
	return cmd
}

func prompt(label string, secret bool) (string, error) {
	fmt.Print(label)
	if secret && isTerminalFn(syscall.Stdin) {
		b, err := readPasswordFn(syscall.Stdin)
		fmt.Println()
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\n"), nil
}
