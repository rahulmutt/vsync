package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		origStore, origClient := storeCredentialsFn, newClientFn
		origAddr, origToken := flagVaultAddr, flagVaultToken
		defer func() {
			storeCredentialsFn = origStore
			newClientFn = origClient
			flagVaultAddr = origAddr
			flagVaultToken = origToken
		}()
		flagVaultAddr = "http://vault"
		flagVaultToken = "token"
		storeCredentialsFn = func(*state.Dirs, []byte, string, string) error { return errors.New("store") }
		newClientFn = func(*vlt.Credentials, int) (*vlt.Client, error) { return nil, errors.New("client") }
		cmd := initCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "store") {
			t.Fatalf("init error = %v, want store", err)
		}
		storeCredentialsFn = func(*state.Dirs, []byte, string, string) error { return nil }
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "client") {
			t.Fatalf("init error = %v, want client", err)
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
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		newClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
			return vlt.NewClient(creds, kvVersion)
		}
		flagVaultAddr = server.URL
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
		loadConfigFn = func(string) (*config.Config, error) { return nil, errors.New("cfg") }
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
	t.Setenv("VSYNC_CONFIG", cfgPath)
	if err := vlt.StoreCredentials(dirs, key, "http://vault", "token"); err != nil {
		t.Fatal(err)
	}
	origLoadCfg, origLoadCreds, origClient, origSecret, origExec := loadConfigFn, loadVaultCredentials, newVaultClient, getCachedEnvSecret, execRealCommand
	defer func() {
		loadConfigFn = origLoadCfg
		loadVaultCredentials = origLoadCreds
		newVaultClient = origClient
		getCachedEnvSecret = origSecret
		execRealCommand = origExec
	}()

	t.Run("config and creds", func(t *testing.T) {
		loadConfigFn = func(string) (*config.Config, error) { return nil, errors.New("cfg") }
		cmd := execCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"pi"})
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "cfg") {
			t.Fatalf("exec error = %v, want cfg", err)
		}
		loadConfigFn = origLoadCfg
		loadVaultCredentials = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) { return nil, errors.New("creds") }
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "creds") {
			t.Fatalf("exec error = %v, want creds", err)
		}
		loadVaultCredentials = origLoadCreds
		newVaultClient = func(*vlt.Credentials, int) (*vlt.Client, error) { return nil, errors.New("client") }
		if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "client") {
			t.Fatalf("exec error = %v, want client", err)
		}
	})

	t.Run("secret fetch and direct exec", func(t *testing.T) {
		newVaultClient = origClient
		getCachedEnvSecret = func(*state.Dirs, []byte, *vlt.Client, string, string) (string, error) { return "", errors.New("secret") }
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
	t.Setenv("VSYNC_CONFIG", cfgPath)
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
		loadConfigFn = func(string) (*config.Config, error) { return nil, errors.New("cfg") }
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
		loadCredsFn = func(*state.Dirs, []byte, string, string) (*vlt.Credentials, error) { return &vlt.Credentials{Addr: "http://vault", Token: "token"}, nil }
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

		cfg := &config.Config{Vault: config.VaultConfig{FilesPrefix: "secret/data/vsync/files"}, Files: []config.FileEntry{{Path: filepath.Join(home, "ok.txt"), Key: "ok", Mode: "0640"}, {Path: "/dev/full", Key: "fail", Mode: "0600"}}}
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
	t.Setenv("VSYNC_CONFIG", cfgPath)
	if err := vlt.StoreCredentials(dirs, key, "http://vault", "token"); err != nil {
		t.Fatal(err)
	}
	origLoadCfg, origClient := loadConfigFn, newVaultClientFn
	defer func() {
		loadConfigFn = origLoadCfg
		newVaultClientFn = origClient
	}()

	t.Run("config error", func(t *testing.T) {
		loadConfigFn = func(string) (*config.Config, error) { return nil, errors.New("cfg") }
		cmd := statusCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("status error = %v", err)
		}
	})

	t.Run("client and ttl errors", func(t *testing.T) {
		loadConfigFn = origLoadCfg
		newVaultClientFn = func(*vlt.Credentials, int) (*vlt.Client, error) { return nil, errors.New("client") }
		cmd := statusCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("status client error = %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		newVaultClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
			return vlt.NewClient(&vlt.Credentials{Addr: server.URL, Token: creds.Token}, kvVersion)
		}
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("status ttl error = %v", err)
		}
	})
}
