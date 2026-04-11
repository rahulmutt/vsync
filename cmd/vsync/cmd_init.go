package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
	"golang.org/x/term"
)

func initCmd() *cobra.Command {
	var rotateKey bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Store Vault credentials encrypted on disk",
		Long: `init generates a local encryption key (if not present) and stores your
Vault address and token encrypted on disk so vsync can connect to Vault
without exposing credentials in plain text.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs, err := state.DefaultDirs()
			if err != nil {
				return err
			}
			if err := dirs.EnsureAll(); err != nil {
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
				if _, err := crypto.GenerateKey(keyPath); err != nil {
					return err
				}
			} else {
				if _, err := crypto.LoadOrGenerateKey(keyPath); err != nil {
					return err
				}
			}
			key, err := crypto.LoadKey(keyPath)
			if err != nil {
				return err
			}

			// Resolve Vault address.
			addr := resolveVaultAddr()
			if addr == "" {
				addr, err = prompt("Vault address (VAULT_ADDR): ", false)
				if err != nil {
					return err
				}
			}
			addr = strings.TrimSpace(addr)
			if addr == "" {
				return fmt.Errorf("vault address is required")
			}

			// Resolve Vault token.
			token := resolveVaultToken()
			if token == "" {
				token, err = prompt("Vault token (VAULT_TOKEN): ", true)
				if err != nil {
					return err
				}
			}
			token = strings.TrimSpace(token)
			if token == "" {
				return fmt.Errorf("vault token is required")
			}

			// Store encrypted.
			if err := vlt.StoreCredentials(dirs, key, addr, token); err != nil {
				return err
			}

			// Verify connectivity.
			fmt.Print("vsync: verifying vault connectivity… ")
			creds := &vlt.Credentials{Addr: addr, Token: token}
			client, err := vlt.NewClient(creds, 2)
			if err != nil {
				fmt.Println("✗")
				return fmt.Errorf("create vault client: %w", err)
			}
			if err := client.Ping(); err != nil {
				fmt.Println("✗")
				fmt.Fprintf(os.Stderr, "vsync: warning: vault ping failed: %v\n", err)
				fmt.Println("vsync: credentials stored, but vault is not reachable right now.")
			} else {
				fmt.Println("✓")
				ttl, err := client.TokenTTL()
				if err == nil && ttl > 0 && ttl.Hours() < 1 {
					fmt.Fprintf(os.Stderr, "vsync: warning: vault token expires in %.0f minutes\n", ttl.Minutes())
				}
			}

			fmt.Printf("vsync: credentials stored at %s\n", dirs.Tokens)
			fmt.Printf("vsync: encryption key at     %s\n", keyPath)
			return nil
		},
	}

	cmd.Flags().BoolVar(&rotateKey, "rotate-key", false, "Generate a new encryption key (re-encrypt existing tokens)")
	return cmd
}

func prompt(label string, secret bool) (string, error) {
	fmt.Print(label)
	if secret && term.IsTerminal(syscall.Stdin) {
		b, err := term.ReadPassword(syscall.Stdin)
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
