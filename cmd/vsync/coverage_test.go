package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsync/vsync/internal/config"
	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

func TestMainExitPath(t *testing.T) {
	origExit := exitFn
	defer func() { exitFn = origExit }()
	called := false
	exitFn = func(code int) {
		called = true
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
	}

	oldArgs := os.Args
	os.Args = []string{"vsync", "--definitely-invalid"}
	main()
	os.Args = oldArgs

	if !called {
		t.Fatal("main() did not call exitFn on error")
	}
}

func TestPromptSecretBranch(t *testing.T) {
	origIsTerminal, origReadPassword := isTerminalFn, readPasswordFn
	defer func() {
		isTerminalFn = origIsTerminal
		readPasswordFn = origReadPassword
	}()
	isTerminalFn = func(int) bool { return true }
	readPasswordFn = func(int) ([]byte, error) { return []byte("secret"), nil }
	got, err := prompt("label: ", true)
	if err != nil {
		t.Fatalf("prompt() error = %v", err)
	}
	if got != "secret" {
		t.Fatalf("prompt() = %q, want secret", got)
	}

	readPasswordFn = func(int) ([]byte, error) { return nil, errors.New("pw") }
	if _, err := prompt("label: ", true); err == nil {
		t.Fatal("prompt() error = nil, want password error")
	}
}

func TestInitCmdErrorBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
	}()

	t.Run("default dirs", func(t *testing.T) {
		orig := defaultDirsFn
		defaultDirsFn = func() (*state.Dirs, error) { return nil, errors.New("dirs") }
		defer func() { defaultDirsFn = orig }()
		cmd := initCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "dirs") {
			t.Fatalf("init error = %v, want dirs", err)
		}
	})

	t.Run("key load", func(t *testing.T) {
		origLoadOrGenerate, origLoadKey := loadOrGenerateKeyFn, loadKeyFn
		defer func() {
			loadOrGenerateKeyFn = origLoadOrGenerate
			loadKeyFn = origLoadKey
		}()
		loadOrGenerateKeyFn = func(string) ([]byte, error) { return nil, errors.New("loadgen") }
		cmd := initCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "loadgen") {
			t.Fatalf("init error = %v, want loadgen", err)
		}

		loadOrGenerateKeyFn = func(string) ([]byte, error) { return []byte("ok"), nil }
		loadKeyFn = func(string) ([]byte, error) { return nil, errors.New("loadkey") }
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "loadkey") {
			t.Fatalf("init error = %v, want loadkey", err)
		}
	})

	t.Run("store and client", func(t *testing.T) {
		origStore, origClient, origPrompt := storeCredentialsFn, newClientFn, promptFn
		origAddr, origToken := flagVaultAddr, flagVaultToken
		defer func() {
			storeCredentialsFn = origStore
			newClientFn = origClient
			promptFn = origPrompt
			flagVaultAddr = origAddr
			flagVaultToken = origToken
		}()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/auth/token/lookup-self" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"auth":{"lease_duration":1800}}`))
		}))
		defer server.Close()

		flagVaultAddr = server.URL
		flagVaultToken = "token"
		promptFn = func(label string, secret bool) (string, error) {
			if secret {
				return "token", nil
			}
			return server.URL, nil
		}
		calls := 0
		newClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("client")
			}
			return vlt.NewClient(creds, kvVersion)
		}
		cmd := initCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("init error = %v", err)
		}
		if calls != 2 {
			t.Fatalf("newClientFn calls = %d, want 2", calls)
		}
		loadedKey, err := crypto.LoadKey(dirs.KeyFile())
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := vlt.LoadCredentials(dirs, loadedKey, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Addr != server.URL || loaded.Token != "token" {
			t.Fatalf("loaded creds = %#v", loaded)
		}
	})

	t.Run("rotate key and ping error", func(t *testing.T) {
		origStore, origClient, origGen := storeCredentialsFn, newClientFn, generateKeyFn
		origAddr, origToken := flagVaultAddr, flagVaultToken
		defer func() {
			storeCredentialsFn = origStore
			newClientFn = origClient
			generateKeyFn = origGen
			flagVaultAddr = origAddr
			flagVaultToken = origToken
		}()
		generateKeyFn = func(string) ([]byte, error) { return nil, errors.New("rotate") }
		flagVaultAddr = "http://vault"
		flagVaultToken = "token"
		cmd := initCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"--rotate-key"})
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "rotate") {
			t.Fatalf("init error = %v, want rotate", err)
		}

		generateKeyFn = origGen
		storeCredentialsFn = func(*state.Dirs, []byte, string, string) error { return nil }
		var lookups int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/auth/token/lookup-self":
				lookups++
				if lookups == 1 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"auth":{"lease_duration":0}}`)
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer server.Close()
		newClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
			return vlt.NewClient(creds, kvVersion)
		}
		flagVaultAddr = server.URL
		promptFn = func(label string, secret bool) (string, error) {
			if secret {
				return "token", nil
			}
			return server.URL, nil
		}
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("init error = %v, want nil", err)
		}
	})
}

func TestShellCmdBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
	}()

	cfgPath := filepath.Join(home, ".config", "vsync", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	cfgContent := "vault:\n  kv_version: 2\nenv:\n  commands:\n    - name: pi\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\nfiles:\n  - path: ~/synced.txt\n    key: example\n"
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSYNC_CONFIG", cfgPath)
	origLaunch, origLoadCfg, origExpand, origEnsure, origCreds, origClient := shellLaunchFn, loadConfigFn, expandPathsFn, ensureShimsFn, loadCredsFn, newVaultClientFn
	defer func() {
		shellLaunchFn = origLaunch
		loadConfigFn = origLoadCfg
		expandPathsFn = origExpand
		ensureShimsFn = origEnsure
		loadCredsFn = origCreds
		newVaultClientFn = origClient
	}()

	t.Run("load config", func(t *testing.T) {
		loadConfigFn = func(string, string) (*config.Config, error) { return nil, errors.New("cfg") }
		cmd := shellCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "cfg") {
			t.Fatalf("shell error = %v, want cfg", err)
		}
	})

	t.Run("expand and ensure", func(t *testing.T) {
		loadConfigFn = origLoadCfg
		expandPathsFn = func(*config.Config) error { return errors.New("expand") }
		cmd := shellCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "expand") {
			t.Fatalf("shell error = %v, want expand", err)
		}
		expandPathsFn = origExpand
		ensureShimsFn = func(*state.Dirs, []string) error { return errors.New("shims") }
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "build shims") {
			t.Fatalf("shell error = %v, want build shims", err)
		}
	})

	t.Run("env shell and missing creds", func(t *testing.T) {
		ensureShimsFn = origEnsure
		loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) { return nil, errors.New("creds") }
		var gotShell string
		shellLaunchFn = func(shellBin, shimDir, keyFile string) error {
			gotShell = shellBin
			if shimDir != dirs.Shims || keyFile != dirs.KeyFile() {
				t.Fatalf("launch args = %q %q", shimDir, keyFile)
			}
			return context.Canceled
		}
		t.Setenv("SHELL", "/bin/zsh")
		cmd := shellCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err != context.Canceled {
			t.Fatalf("shell error = %v, want context.Canceled", err)
		}
		if gotShell != "/bin/zsh" {
			t.Fatalf("shell = %q, want /bin/zsh", gotShell)
		}
	})

	t.Run("default shell fallback and client error", func(t *testing.T) {
		loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) {
			return &vlt.Credentials{Addr: "http://vault", Token: "token"}, nil
		}
		newVaultClientFn = func(*vlt.Credentials, int) (*vlt.Client, error) { return nil, errors.New("client") }
		t.Setenv("SHELL", "")
		var gotShell string
		shellLaunchFn = func(shellBin, shimDir, keyFile string) error {
			gotShell = shellBin
			return context.Canceled
		}
		cmd := shellCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err != context.Canceled {
			t.Fatalf("shell error = %v, want context.Canceled", err)
		}
		if gotShell != "/bin/sh" {
			t.Fatalf("shell fallback = %q, want /bin/sh", gotShell)
		}
	})
}

func TestExecCmdBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
	}()
	cfgPath := filepath.Join(home, ".config", "vsync", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nenv:\n  commands:\n    - name: pi\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSYNC_CONFIG", "")
	if err := vlt.StoreCredentials(dirs, key, "http://vault", "token"); err != nil {
		t.Fatal(err)
	}
	origLoadCfg, origLoadCreds, origClient, origSecret, origExec := loadConfigFn, loadCredsFn, newVaultClientFn, getCachedEnvSecret, execRealCommand
	defer func() {
		loadConfigFn = origLoadCfg
		loadCredsFn = origLoadCreds
		newVaultClientFn = origClient
		getCachedEnvSecret = origSecret
		execRealCommand = origExec
	}()

	t.Run("config and creds", func(t *testing.T) {
		loadConfigFn = func(string, string) (*config.Config, error) { return nil, errors.New("cfg") }
		cmd := execCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"pi"})
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "cfg") {
			t.Fatalf("exec error = %v, want cfg", err)
		}
		loadConfigFn = origLoadCfg
		loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) { return nil, errors.New("creds") }
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "creds") {
			t.Fatalf("exec error = %v, want creds", err)
		}
		loadCredsFn = origLoadCreds
		newVaultClientFn = func(*vlt.Credentials, int) (*vlt.Client, error) { return nil, errors.New("client") }
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "client") {
			t.Fatalf("exec error = %v, want client", err)
		}
	})

	t.Run("secret fetch and direct exec", func(t *testing.T) {
		newVaultClientFn = origClient
		getCachedEnvSecret = func(*state.Dirs, []byte, *vlt.Client, string, string, ...string) (string, error) {
			return "", errors.New("secret")
		}
		cmd := execCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"pi"})
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "secret") {
			t.Fatalf("exec error = %v, want secret", err)
		}
	})
}

func TestSyncCmdBranchesAndHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
	}()
	cfgPath := filepath.Join(home, ".config", "vsync", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nfiles:\n  - path: ~/sync.txt\n    key: sync\n    mode: \"0640\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSYNC_CONFIG", "")
	if err := vlt.StoreCredentials(dirs, key, "http://vault", "token"); err != nil {
		t.Fatal(err)
	}
	origLoadCfg, origExpand, origClient, origCreds, origReadCache := loadConfigFn, expandPathsFn, newVaultClientFn, loadCredsFn, vlt.ReadCache
	defer func() {
		loadConfigFn = origLoadCfg
		expandPathsFn = origExpand
		newVaultClientFn = origClient
		loadCredsFn = origCreds
		_ = origReadCache
	}()

	t.Run("config and expand", func(t *testing.T) {
		loadConfigFn = func(string, string) (*config.Config, error) { return nil, errors.New("cfg") }
		cmd := syncCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "cfg") {
			t.Fatalf("sync error = %v, want cfg", err)
		}
		loadConfigFn = origLoadCfg
		expandPathsFn = func(*config.Config) error { return errors.New("expand") }
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "expand") {
			t.Fatalf("sync error = %v, want expand", err)
		}
	})

	t.Run("creds and client", func(t *testing.T) {
		expandPathsFn = origExpand
		loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) { return nil, errors.New("creds") }
		cmd := syncCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "creds") {
			t.Fatalf("sync error = %v, want creds", err)
		}
		loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) {
			return &vlt.Credentials{Addr: "http://vault", Token: "token"}, nil
		}
		newVaultClientFn = func(*vlt.Credentials, int) (*vlt.Client, error) { return nil, errors.New("client") }
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "client") {
			t.Fatalf("sync error = %v, want client", err)
		}
	})

	t.Run("writeFile and syncFiles", func(t *testing.T) {
		badParent := filepath.Join(t.TempDir(), "parent")
		if err := os.WriteFile(badParent, []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(filepath.Join(badParent, "child.txt"), "0600", []byte("x")); err == nil {
			t.Fatal("writeFile() error = nil, want mkdir failure")
		}
		if err := writeFile("/dev/full", "0600", []byte(strings.Repeat("x", 1024))); err == nil {
			t.Fatal("writeFile() error = nil, want write failure")
		}

		cfg := &config.Config{Vault: config.VaultConfig{VaultProfileConfig: config.VaultProfileConfig{FilesPrefix: "secret/data/vsync/files"}}, Files: []config.FileEntry{{Path: filepath.Join(home, "ok.txt"), Key: "ok", Mode: "0640"}, {Path: "/dev/full", Key: "fail", Mode: "0600"}}}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/secret/data/vsync/files/ok":
				fmt.Fprint(w, `{"data":{"data":{"content":"hello"},"metadata":{}}}`)
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer server.Close()
		client, err := vlt.NewClient(&vlt.Credentials{Addr: server.URL, Token: "token"}, 2)
		if err != nil {
			t.Fatal(err)
		}
		syncFiles(dirs, key, client, cfg)
		data, err := os.ReadFile(filepath.Join(home, "ok.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "hello" {
			t.Fatalf("synced data = %q, want hello", data)
		}
	})
}

func TestStatusCmdBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
	}()
	cfgPath := filepath.Join(home, ".config", "vsync", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nenv:\n  commands:\n    - name: pi\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\nfiles:\n  - path: ~/sync.txt\n    key: sync\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSYNC_CONFIG", "")
	if err := vlt.StoreCredentials(dirs, key, "http://vault", "token"); err != nil {
		t.Fatal(err)
	}
	origLoadCfg, origLoadCreds, origClient := loadConfigFn, loadCredsFn, newVaultClientFn
	defer func() {
		loadConfigFn = origLoadCfg
		loadCredsFn = origLoadCreds
		newVaultClientFn = origClient
		flagKeyPath = ""
	}()

	t.Run("config error", func(t *testing.T) {
		loadConfigFn = func(string, string) (*config.Config, error) { return nil, errors.New("cfg") }
		cmd := statusCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("status error = %v", err)
		}
	})

	t.Run("credential and cache branches", func(t *testing.T) {
		loadConfigFn = origLoadCfg
		flagKeyPath = filepath.Join(home, "status.key")
		if _, err := crypto.GenerateKey(flagKeyPath); err != nil {
			t.Fatal(err)
		}

		// Create a present shim for pi.
		if err := os.MkdirAll(dirs.Shims, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dirs.ShimFile("pi"), []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
		// Create env cache entries covering expired, no expiry and fresh branches.
		if err := vlt.WriteCache(dirs, key, "env", "gemini-api-key", &vlt.CacheEntry{Value: "expired", ExpiresAt: time.Now().Add(-time.Minute), VaultPath: "secret/data/vsync/env/gemini-api-key"}); err != nil {
			t.Fatal(err)
		}
		if err := vlt.WriteCache(dirs, key, "files", "sync", &vlt.CacheEntry{Value: "file-cache", ExpiresAt: time.Now().Add(time.Hour), VaultPath: "secret/data/vsync/files/sync"}); err != nil {
			t.Fatal(err)
		}
		if err := vlt.WriteCache(dirs, key, "env", "no-expiry", &vlt.CacheEntry{Value: "no-expiry", VaultPath: "secret/data/vsync/env/no-expiry"}); err != nil {
			t.Fatal(err)
		}

		loadConfigFn = func(string, string) (*config.Config, error) {
			cfg := &config.Config{}
			cfg.Env.Commands = []config.CommandEntry{{Name: "pi", Variables: []config.VariableEntry{{Name: "GEMINI_API_KEY", Key: "gemini-api-key"}, {Name: "NO_EXPIRY", Key: "no-expiry"}}}}
			cfg.Files = []config.FileEntry{{Path: filepath.Join(home, "missing.txt"), Key: "sync"}}
			return cfg, nil
		}

		// Unbounded TTL path.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/auth/token/lookup-self":
				_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{"lease_duration": 0}})
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer server.Close()
		loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) {
			return &vlt.Credentials{Addr: strings.Repeat("http://very-long-vault-address.example/", 2), Token: "token-with-a-very-long-address-to-trigger-truncation"}, nil
		}
		newVaultClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
			return vlt.NewClient(&vlt.Credentials{Addr: server.URL, Token: creds.Token}, kvVersion)
		}
		cmd := statusCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("status unlimited ttl error = %v", err)
		}

		// TTL parse error path.
		serverErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/auth/token/lookup-self":
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ttl": "bad-duration"}})
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer serverErr.Close()
		loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) {
			return &vlt.Credentials{Addr: strings.Repeat("http://very-long-vault-address.example/", 2), Token: "token"}, nil
		}
		newVaultClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
			return vlt.NewClient(&vlt.Credentials{Addr: serverErr.URL, Token: creds.Token}, kvVersion)
		}
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("status ttl parse error = %v", err)
		}
	})
}

func TestRootPromptResolveAndShellKeyBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
		flagKeyPath = ""
	}()

	flagKeyPath = filepath.Join(home, "missing.key")
	t.Setenv("VSYNC_KEY", "")
	if _, err := resolveKey(dirs); err == nil {
		t.Fatal("resolveKey() error = nil, want missing key error")
	}

	root := rootCmd()
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.SetArgs([]string{"--key", filepath.Join(home, "missing.key"), "status"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("root.ExecuteContext(status) error = nil, want missing key error")
	}

	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	_ = w.Close()
	defer func() { os.Stdin = oldStdin }()
	if _, err := prompt("label: ", false); err == nil {
		t.Fatal("prompt() error = nil, want EOF")
	}

	origIsTerminal, origReadPassword := isTerminalFn, readPasswordFn
	defer func() {
		isTerminalFn = origIsTerminal
		readPasswordFn = origReadPassword
	}()
	isTerminalFn = func(int) bool { return true }
	readPasswordFn = func(int) ([]byte, error) { return nil, errors.New("pw") }
	if _, err := prompt("label: ", true); err == nil {
		t.Fatal("prompt(secret) error = nil, want password error")
	}
}

func TestShellSyncSyncAndCacheBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
		flagKeyPath = ""
		userHomeDirFn = os.UserHomeDir
	}()
	cfgPath := filepath.Join(home, ".config", "vsync", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("vault:\n  kv_version: 2\nenv:\n  commands:\n    - name: pi\n      variables:\n        - name: GEMINI_API_KEY\n          key: gemini-api-key\nfiles:\n  - path: ~/sync.txt\n    key: sync\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSYNC_CONFIG", "")
	if err := vlt.StoreCredentials(dirs, key, "http://vault", "token"); err != nil {
		t.Fatal(err)
	}

	origResolveConfig, origLoadCfg, origExpand, origEnsure, origCreds, origClient, origLaunch := resolveConfigPath, loadConfigFn, expandPathsFn, ensureShimsFn, loadCredsFn, newVaultClientFn, shellLaunchFn
	defer func() {
		loadConfigFn = origLoadCfg
		expandPathsFn = origExpand
		ensureShimsFn = origEnsure
		loadCredsFn = origCreds
		newVaultClientFn = origClient
		shellLaunchFn = origLaunch
		_ = origResolveConfig
	}()

	t.Run("shell config path error and key override", func(t *testing.T) {
		userHomeDirFn = func() (string, error) { return "", errors.New("home") }
		cmd := shellCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("shellCmd() error = nil, want home error")
		}
		userHomeDirFn = os.UserHomeDir
		flagKeyPath = filepath.Join(home, "override.key")
		if _, err := crypto.GenerateKey(flagKeyPath); err != nil {
			t.Fatal(err)
		}
		loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) {
			return &vlt.Credentials{Addr: "http://vault", Token: "token"}, nil
		}
		newVaultClientFn = func(*vlt.Credentials, int) (*vlt.Client, error) { return nil, errors.New("client") }
		shellLaunchFn = func(shellBin, shimDir, keyFile string) error {
			if keyFile != flagKeyPath {
				t.Fatalf("keyFile = %q, want %q", keyFile, flagKeyPath)
			}
			return context.Canceled
		}
		cmd = shellCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"--shell", "/bin/zsh"})
		if err := cmd.ExecuteContext(context.Background()); err != context.Canceled {
			t.Fatalf("shellCmd() error = %v, want context.Canceled", err)
		}
	})

	t.Run("sync resolve config, file filter and force error", func(t *testing.T) {
		userHomeDirFn = func() (string, error) { return "", errors.New("home") }
		cmd := syncCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("syncCmd() error = nil, want home error")
		}
		userHomeDirFn = os.UserHomeDir
		loadConfigFn = origLoadCfg
		expandPathsFn = origExpand
		newVaultClientFn = func(*vlt.Credentials, int) (*vlt.Client, error) { return nil, errors.New("client") }
		loadCredsFn = origCreds
		cmd = syncCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"--file", "sync"})
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("syncCmd(--file) error = nil, want client or fetch error")
		}

		// Force path with a successful client but failing fetch should still continue.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		newVaultClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
			return vlt.NewClient(&vlt.Credentials{Addr: server.URL, Token: creds.Token}, kvVersion)
		}
		cmd = syncCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"--force"})
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("syncCmd(--force) error = %v", err)
		}
	})

	t.Run("cache clear key/env/files and errors", func(t *testing.T) {
		// key clear success
		if err := vlt.WriteCache(dirs, key, "env", "k", &vlt.CacheEntry{Value: "x"}); err != nil {
			t.Fatal(err)
		}
		cmd := cacheCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"clear", "--key", "k"})
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("cache clear key error = %v", err)
		}

		if err := vlt.WriteCache(dirs, key, "env", "a", &vlt.CacheEntry{Value: "x"}); err != nil {
			t.Fatal(err)
		}
		if err := vlt.WriteCache(dirs, key, "files", "b", &vlt.CacheEntry{Value: "x"}); err != nil {
			t.Fatal(err)
		}
		cmd = cacheCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"clear", "--env", "--files"})
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("cache clear env/files error = %v", err)
		}

		cmd = cacheCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"clear"})
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("cache clear error = nil, want missing flags error")
		}

		// provoke DeleteCache error.
		delPath := dirs.CacheFile("env", "fail")
		if err := os.MkdirAll(delPath, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(delPath, "child"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		cmd = cacheCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"clear", "--key", "fail"})
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("cache clear delete error = nil, want delete failure")
		}

		// provoke ClearCacheKind errors by turning cache subdirs into files.
		badBase := t.TempDir()
		badDirs := &state.Dirs{Base: badBase, Keys: filepath.Join(badBase, "keys"), Tokens: filepath.Join(badBase, "tokens"), Cache: filepath.Join(badBase, "cache"), Shims: filepath.Join(badBase, "shims")}
		if err := os.MkdirAll(badDirs.Cache, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(badDirs.Cache, "env"), []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
		oldDirs := globalDirs
		globalDirs = badDirs
		cmd = cacheCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"clear", "--env"})
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("cache clear env error = nil, want read-dir failure")
		}
		globalDirs = oldDirs
	})
}

func TestInitCmdEnsureAllAndPromptErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")

	badBase := t.TempDir()
	badDirs := &state.Dirs{Base: badBase, Keys: filepath.Join(badBase, "keys"), Tokens: filepath.Join(badBase, "tokens"), Cache: filepath.Join(badBase, "cache"), Shims: filepath.Join(badBase, "shims")}
	if err := os.WriteFile(badDirs.Keys, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}

	origDefaultDirs, origStore, origClient, origPrompt := defaultDirsFn, storeCredentialsFn, newClientFn, promptFn
	defer func() {
		defaultDirsFn = origDefaultDirs
		storeCredentialsFn = origStore
		newClientFn = origClient
		promptFn = origPrompt
		flagVaultAddr = ""
		flagVaultToken = ""
	}()

	defaultDirsFn = func() (*state.Dirs, error) { return badDirs, nil }
	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("initCmd() error = nil, want EnsureAll failure")
	}

	goodHome := t.TempDir()
	t.Setenv("HOME", goodHome)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = make([]byte, 32)
	defaultDirsFn = state.DefaultDirs
	promptFn = func(label string, secret bool) (string, error) { return "", errors.New("prompt") }
	flagVaultAddr = ""
	flagVaultToken = ""
	if err := initCmd().ExecuteContext(context.Background()); err == nil {
		t.Fatal("initCmd() addr prompt error = nil, want prompt failure")
	}
	flagVaultAddr = "http://vault"
	if err := initCmd().ExecuteContext(context.Background()); err == nil {
		t.Fatal("initCmd() token prompt error = nil, want prompt failure")
	}
}

func TestExecCmdResolveConfigPathError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	origHome := userHomeDirFn
	userHomeDirFn = func() (string, error) { return "", errors.New("home") }
	defer func() { userHomeDirFn = origHome }()
	cmd := execCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"tool"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("execCmd() error = nil, want home error")
	}
}

func TestExecCmdDirectBranch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
	}()
	cfgPath := filepath.Join(home, ".config", "vsync", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`env:
  commands: []
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSYNC_CONFIG", cfgPath)
	origLoadCfg, origExec := loadConfigFn, execRealCommand
	defer func() {
		loadConfigFn = origLoadCfg
		execRealCommand = origExec
	}()
	loadConfigFn = func(string, string) (*config.Config, error) { return &config.Config{}, nil }
	called := false
	execRealCommand = func(name string, args []string, extraEnv map[string]string, shimDir string) error {
		called = true
		if name != "tool" || shimDir != dirs.Shims || len(extraEnv) != 0 {
			t.Fatalf("execRealCommand called with %q, %q, %#v", name, shimDir, extraEnv)
		}
		return context.Canceled
	}
	if err := execConfiguredCommand(&config.Config{}, "tool", []string{"a", "b"}, dirs, key); err != context.Canceled {
		t.Fatalf("execConfiguredCommand() error = %v, want context.Canceled", err)
	}
	if !called {
		t.Fatal("execRealCommand was not called")
	}
}

func TestSyncCmdAdditionalBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs, err := state.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey(dirs.KeyFile())
	if err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	globalKey = key
	defer func() {
		globalDirs = nil
		globalKey = nil
		flagVaultAddr = ""
		flagVaultToken = ""
	}()
	cfgPath := filepath.Join(home, ".config", "vsync", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`vault:
  kv_version: 2
files:
  - path: ~/sync.txt
    key: sync
  - path: /dev/full
    key: full
  - path: ~/fail.txt
    key: fail
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSYNC_CONFIG", cfgPath)
	if err := vlt.StoreCredentials(dirs, key, "http://vault", "token"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(t.TempDir(), "default.txt"), "", []byte("default")); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(t.TempDir(), "0600", []byte("x")); err == nil {
		t.Fatal("writeFile() error = nil, want open failure")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/secret/data/vsync/files/sync":
			fmt.Fprint(w, `{"data":{"data":{"content":"hello"},"metadata":{}}}`)
		case "/v1/secret/data/vsync/files/full":
			fmt.Fprint(w, `{"data":{"data":{"content":"full"},"metadata":{}}}`)
		case "/v1/secret/data/vsync/files/fail":
			fmt.Fprint(w, `{"data":{"data":{},"metadata":{}}}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	origLoadCreds, origNewClient := loadCredsFn, newVaultClientFn
	defer func() {
		loadCredsFn = origLoadCreds
		newVaultClientFn = origNewClient
	}()
	loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) {
		return &vlt.Credentials{Addr: server.URL, Token: "token"}, nil
	}
	newVaultClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
		return vlt.NewClient(creds, kvVersion)
	}

	cmd := syncCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--file", "sync"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("syncCmd(--file) error = %v", err)
	}

	cmd = syncCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("syncCmd() error = %v", err)
	}

	cmd = syncCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--force"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("syncCmd(--force) error = %v", err)
	}

	client, err := vlt.NewClient(&vlt.Credentials{Addr: server.URL, Token: "token"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	syncFiles(dirs, key, client, &config.Config{Vault: config.VaultConfig{VaultProfileConfig: config.VaultProfileConfig{FilesPrefix: "secret/data/vsync/files"}}, Files: []config.FileEntry{{Path: "/dev/full", Key: "full"}}})
}

func TestCacheCmdFilesErrorBranch(t *testing.T) {
	base := t.TempDir()
	dirs := &state.Dirs{Base: base, Keys: filepath.Join(base, "keys"), Tokens: filepath.Join(base, "tokens"), Cache: filepath.Join(base, "cache"), Shims: filepath.Join(base, "shims")}
	if err := os.MkdirAll(dirs.Cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Cache, "files"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	defer func() { globalDirs = nil }()
	cmd := cacheCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"clear", "--files"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("cache clear --files error = nil, want read-dir failure")
	}
}
