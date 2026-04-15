package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsync/vsync/internal/crypto"
	"github.com/vsync/vsync/internal/state"
	vlt "github.com/vsync/vsync/internal/vault"
)

func setupInitTest(t *testing.T) (*state.Dirs, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VSYNC_KEY", "")

	dirs, err := defaultDirsFn()
	if err != nil {
		t.Fatal(err)
	}
	if err := dirs.EnsureAll(); err != nil {
		t.Fatal(err)
	}
	globalDirs = dirs
	t.Cleanup(func() {
		globalDirs = nil
		flagGlobalConfigPath = ""
		flagConfigPath = ""
		flagVaultAddr = ""
		flagVaultToken = ""
		flagVaultEnvPrefix = ""
		flagVaultFilesPrefix = ""
		flagVaultKVVersion = ""
		flagKeyPath = ""
		defaultDirsFn = state.DefaultDirs
		generateKeyFn = crypto.GenerateKey
		loadOrGenerateKeyFn = crypto.LoadOrGenerateKey
		loadKeyFn = crypto.LoadKey
		storeCredentialsFn = vlt.StoreCredentials
		storeProfileCredsFn = vlt.StoreCredentialsForProfile
		newClientFn = vlt.NewClient
		resolveVaultAddrFn = resolveVaultAddr
		resolveVaultTokenFn = resolveVaultToken
		promptFn = prompt
	})
	return dirs, home
}

func TestInitCmdStoresCredentialsAndUsesProvidedEnv(t *testing.T) {
	dirs, home := setupInitTest(t)
	keyPath := filepath.Join(home, "custom.key")
	flagKeyPath = keyPath
	flagVaultAddr = "http://127.0.0.1:8200"
	flagVaultToken = "dev-token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/token/lookup-self":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"auth":{"lease_duration":1800}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	flagVaultAddr = server.URL

	var stdout bytes.Buffer
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wOut
	os.Stderr = wErr
	defer func() {
		os.Stdout = oldOut
		os.Stderr = oldErr
	}()
	outC := make(chan string, 1)
	errC := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(rOut)
		outC <- string(data)
	}()
	go func() {
		data, _ := io.ReadAll(rErr)
		errC <- string(data)
	}()

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initCmd() error = %v", err)
	}
	_ = wOut.Close()
	_ = wErr.Close()
	stdout.WriteString(<-outC)
	stdout.WriteString(<-errC)

	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file missing: %v", err)
	}
	key, err := crypto.LoadKey(keyPath)
	if err != nil {
		t.Fatalf("LoadKey() error = %v", err)
	}
	creds, err := vlt.LoadCredentials(dirs, key, "", "")
	if err != nil {
		t.Fatalf("LoadCredentials() error = %v", err)
	}
	if creds.Addr != server.URL || creds.Token != "dev-token" {
		t.Fatalf("stored creds = %#v", creds)
	}
	if !strings.Contains(stdout.String(), "cache stored at") {
		t.Fatalf("init output missing cache dir line:\n%s", stdout.String())
	}
}

func TestInitCmdStoresMultipleProfiles(t *testing.T) {
	dirs, home := setupInitTest(t)
	keyPath := filepath.Join(home, "multi.key")
	flagKeyPath = keyPath
	flagVaultAddr = "http://127.0.0.1:8200"
	flagVaultToken = "default-token"

	cfgDir := filepath.Join(home, ".config", "vsync")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/token/lookup-self" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth":{"lease_duration":1800}}`))
	}))
	defer server.Close()
	if err := os.WriteFile(cfgPath, []byte("vault:\n  addr: "+server.URL+"\n  token: default-token\n  profiles:\n    prod:\n      env_prefix: prod/env\n"), 0600); err != nil {
		t.Fatal(err)
	}
	flagVaultAddr = server.URL

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initCmd() error = %v", err)
	}

	key, err := crypto.LoadKey(keyPath)
	if err != nil {
		t.Fatalf("LoadKey() error = %v", err)
	}
	defaultCreds, err := vlt.LoadCredentials(dirs, key, "", "")
	if err != nil {
		t.Fatalf("LoadCredentials(default) error = %v", err)
	}
	if defaultCreds.Addr != server.URL || defaultCreds.Token != "default-token" {
		t.Fatalf("default creds = %#v", defaultCreds)
	}
	prodCreds, err := vlt.LoadCredentialsForProfile(dirs, key, "prod", "", "")
	if err != nil {
		t.Fatalf("LoadCredentialsForProfile(prod) error = %v", err)
	}
	if prodCreds.Addr != server.URL || prodCreds.Token != "default-token" {
		t.Fatalf("prod creds = %#v", prodCreds)
	}
	if _, err := os.Stat(dirs.ProfileTokenFile("prod", "vault_addr")); err != nil {
		t.Fatalf("profile addr file missing: %v", err)
	}
}

func TestInitCmdInheritsDefaultCredentialsForNamedProfiles(t *testing.T) {
	dirs, home := setupInitTest(t)
	keyPath := filepath.Join(home, "retry.key")
	flagKeyPath = keyPath

	cfgDir := filepath.Join(home, ".config", "vsync")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(`vault:
  profiles:
    work:
      kv_version: 2
`), 0600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/token/lookup-self" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		token := r.Header.Get("X-Vault-Token")
		if token != "default-token" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth":{"lease_duration":1800}}`))
	}))
	defer server.Close()

	key, err := crypto.GenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := vlt.StoreCredentials(dirs, key, server.URL, "default-token"); err != nil {
		t.Fatal(err)
	}
	if err := vlt.StoreCredentialsForProfile(dirs, key, "work", server.URL, "bad-token"); err != nil {
		t.Fatal(err)
	}

	flagVaultAddr = server.URL
	flagVaultToken = "default-token"

	promptCalls := 0
	promptFn = func(label string, secret bool) (string, error) {
		promptCalls++
		t.Fatalf("promptFn should not be called, got label %s", label)
		return "", nil
	}

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initCmd() error = %v", err)
	}
	if promptCalls != 0 {
		t.Fatalf("promptFn calls = %d, want 0", promptCalls)
	}

	loaded, err := vlt.LoadCredentialsForProfile(dirs, key, "work", "", "")
	if err != nil {
		t.Fatalf("LoadCredentialsForProfile(work) error = %v", err)
	}
	if loaded.Addr != server.URL || loaded.Token != "default-token" {
		t.Fatalf("work creds = %#v, want inherited default creds", loaded)
	}
}

func TestInitCmdConfiguresDefaultProfileBeforeNamedProfiles(t *testing.T) {
	dirs, home := setupInitTest(t)
	keyPath := filepath.Join(home, "order.key")
	flagKeyPath = keyPath
	flagVaultAddr = "http://127.0.0.1:8200"
	flagVaultToken = "default-token"

	cfgDir := filepath.Join(home, ".config", "vsync")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(`vault:
  profiles:
    zeta:
      kv_version: 2
    alpha:
      kv_version: 2
`), 0600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/token/lookup-self" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Vault-Token"); got != "default-token" {
			t.Fatalf("unexpected token: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth":{"lease_duration":1800}}`))
	}))
	defer server.Close()
	flagVaultAddr = server.URL

	key, err := crypto.GenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	origStoreDefault, origStoreProfile := storeCredentialsFn, storeProfileCredsFn
	defer func() {
		storeCredentialsFn = origStoreDefault
		storeProfileCredsFn = origStoreProfile
	}()
	calls := make([]string, 0, 3)
	storeCredentialsFn = func(_ *state.Dirs, _ []byte, addr, token string) error {
		calls = append(calls, "default")
		if addr != server.URL || token != "default-token" {
			t.Fatalf("default creds = %q %q, want %q %q", addr, token, server.URL, "default-token")
		}
		return vlt.StoreCredentials(dirs, key, addr, token)
	}
	storeProfileCredsFn = func(_ *state.Dirs, _ []byte, profile, addr, token string) error {
		calls = append(calls, profile)
		if profile == "alpha" || profile == "zeta" {
			if addr != server.URL || token != "default-token" {
				t.Fatalf("%s creds = %q %q, want inherited default", profile, addr, token)
			}
			return vlt.StoreCredentialsForProfile(dirs, key, profile, addr, token)
		}
		return fmt.Errorf("unexpected profile %q", profile)
	}

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initCmd() error = %v", err)
	}
	if got, want := strings.Join(calls, ","), "default,alpha,zeta"; got != want {
		t.Fatalf("store order = %q, want %q", got, want)
	}
}

func TestPromptReadsLineFromStdin(t *testing.T) {
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		_, _ = io.WriteString(w, "hello\n")
		_ = w.Close()
	}()
	got, err := prompt("label: ", false)
	if err != nil {
		t.Fatalf("prompt() error = %v", err)
	}
	if got != "hello" {
		t.Fatalf("prompt() = %q, want hello", got)
	}
}

func TestInitCmdRejectsBlankAddrAndToken(t *testing.T) {
	_, home := setupInitTest(t)
	keyPath := filepath.Join(home, "blank.key")
	flagKeyPath = keyPath
	promptFn = func(label string, secret bool) (string, error) {
		if secret {
			return "", nil
		}
		return "   ", nil
	}

	flagVaultAddr = "   "
	flagVaultToken = ""
	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "vault address is required") {
		t.Fatalf("initCmd() addr error = %v, want vault address required", err)
	}

	flagVaultAddr = "http://127.0.0.1:8200"
	flagVaultToken = "   "
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "vault token is required") {
		t.Fatalf("initCmd() token error = %v, want vault token required", err)
	}
}

func TestInitCmdRotateKeyRegeneratesKey(t *testing.T) {
	_, home := setupInitTest(t)
	keyPath := filepath.Join(home, "rotated.key")
	flagKeyPath = keyPath
	flagVaultAddr = "http://127.0.0.1:8200"
	flagVaultToken = "dev-token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth":{"lease_duration":3600}}`))
	}))
	defer server.Close()
	flagVaultAddr = server.URL

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("first initCmd() error = %v", err)
	}
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	cmd = initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--rotate-key"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("rotate initCmd() error = %v", err)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(after) {
		t.Fatal("rotate-key did not change key contents")
	}
}

func TestInitCmdUsesStoredCredentialsWhenConfigOmitsThem(t *testing.T) {
	dirs, home := setupInitTest(t)
	keyPath := filepath.Join(home, "stored.key")
	flagKeyPath = keyPath

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/token/lookup-self" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth":{"lease_duration":1200}}`))
	}))
	defer server.Close()

	key, err := crypto.GenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := vlt.StoreCredentials(dirs, key, server.URL, "stored-token"); err != nil {
		t.Fatal(err)
	}
	promptFn = func(string, bool) (string, error) {
		t.Fatal("promptFn should not be called when stored credentials exist")
		return "", nil
	}

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initCmd() with stored credentials error = %v", err)
	}

	loaded, err := vlt.LoadCredentials(dirs, key, "", "")
	if err != nil {
		t.Fatalf("LoadCredentials() error = %v", err)
	}
	if loaded.Addr != server.URL || loaded.Token != "stored-token" {
		t.Fatalf("loaded creds = %#v", loaded)
	}
}

func TestInitCmdRepromptsAfterClientCreationFails(t *testing.T) {
	dirs, home := setupInitTest(t)
	keyPath := filepath.Join(home, "clientfail.key")
	flagKeyPath = keyPath
	flagVaultAddr = "http://127.0.0.1:8200"
	flagVaultToken = "token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/token/lookup-self" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth":{"lease_duration":1800}}`))
	}))
	defer server.Close()
	flagVaultAddr = server.URL

	origClient, origPrompt := newClientFn, promptFn
	defer func() {
		newClientFn = origClient
		promptFn = origPrompt
	}()
	calls := 0
	newClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
		calls++
		if calls == 1 {
			return nil, fmt.Errorf("client boom")
		}
		return vlt.NewClient(creds, kvVersion)
	}
	promptFn = func(label string, secret bool) (string, error) {
		switch {
		case strings.Contains(label, "Vault address (default)"):
			return server.URL, nil
		case strings.Contains(label, "Vault token (default)"):
			return "good-token", nil
		default:
			t.Fatalf("unexpected prompt label: %s", label)
			return "", nil
		}
	}

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initCmd() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("newClientFn calls = %d, want 2", calls)
	}
	key, err := crypto.LoadKey(keyPath)
	if err != nil {
		t.Fatalf("LoadKey() error = %v", err)
	}
	loaded, err := vlt.LoadCredentials(dirs, key, "", "")
	if err != nil {
		t.Fatalf("LoadCredentials() error = %v", err)
	}
	if loaded.Addr != server.URL || loaded.Token != "good-token" {
		t.Fatalf("loaded creds = %#v", loaded)
	}
}
