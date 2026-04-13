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

func setupCoverageState(t *testing.T) (*state.Dirs, []byte) {
	t.Helper()

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
	flagVaultAddr = ""
	flagVaultToken = ""
	flagVaultEnvPrefix = ""
	flagVaultFilesPrefix = ""
	flagVaultKVVersion = ""
	flagGlobalConfigPath = ""
	flagConfigPath = ""
	flagKeyPath = ""
	t.Cleanup(func() {
		globalDirs = nil
		globalKey = nil
		flagVaultAddr = ""
		flagVaultToken = ""
		flagVaultEnvPrefix = ""
		flagVaultFilesPrefix = ""
		flagVaultKVVersion = ""
		flagGlobalConfigPath = ""
		flagConfigPath = ""
		flagKeyPath = ""
	})

	return dirs, key
}

func writeCoverageConfig(t *testing.T, content string) string {
	t.Helper()

	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "vsync", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestInitCmdResolveGlobalConfigPathError(t *testing.T) {
	setupCoverageState(t)

	origHome := userHomeDirFn
	defer func() { userHomeDirFn = origHome }()
	userHomeDirFn = func() (string, error) { return "", errors.New("home") }

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "home") {
		t.Fatalf("initCmd() error = %v, want home error", err)
	}
}

func TestInitCmdConfigLoadError(t *testing.T) {
	setupCoverageState(t)
	writeCoverageConfig(t, "vault:\n  kv_version: 2\n")

	origLoadCfg := loadConfigFn
	defer func() { loadConfigFn = origLoadCfg }()
	loadConfigFn = func(string, string) (*config.Config, error) { return nil, errors.New("cfg") }

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "cfg") {
		t.Fatalf("initCmd() error = %v, want cfg error", err)
	}
}

func TestInitCmdUsesStoredDefaultAndProfileCredentials(t *testing.T) {
	dirs, key := setupCoverageState(t)
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")

	cfg := &config.Config{
		Vault: config.VaultConfig{
			VaultProfileConfig: config.VaultProfileConfig{KVVersion: 2},
			Profiles:           map[string]config.VaultProfileConfig{"work": {KVVersion: 2}},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/token/lookup-self":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"auth":{"lease_duration":0}}`)
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	if err := vlt.StoreCredentials(dirs, key, server.URL, "default-token"); err != nil {
		t.Fatal(err)
	}
	if err := vlt.StoreCredentialsForProfile(dirs, key, "work", server.URL, "work-token"); err != nil {
		t.Fatal(err)
	}

	origLoadCfg, origStoreDefault, origStoreProfile, origNewClient := loadConfigFn, storeCredentialsFn, storeProfileCredsFn, newClientFn
	defer func() {
		loadConfigFn = origLoadCfg
		storeCredentialsFn = origStoreDefault
		storeProfileCredsFn = origStoreProfile
		newClientFn = origNewClient
	}()
	loadConfigFn = func(string, string) (*config.Config, error) { return cfg, nil }

	defaultCalled := false
	profileCalled := false
	storeCredentialsFn = func(_ *state.Dirs, _ []byte, addr, token string) error {
		defaultCalled = true
		if addr != server.URL || token != "default-token" {
			t.Fatalf("storeCredentialsFn got %q %q, want %q default-token", addr, token, server.URL)
		}
		return nil
	}
	storeProfileCredsFn = func(_ *state.Dirs, _ []byte, profile, addr, token string) error {
		profileCalled = true
		if profile != "work" || addr != server.URL || token != "work-token" {
			t.Fatalf("storeProfileCredsFn got %q %q %q, want work %q work-token", profile, addr, token, server.URL)
		}
		return nil
	}

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initCmd() error = %v", err)
	}
	if !defaultCalled {
		t.Fatal("storeCredentialsFn was not called for default profile")
	}
	if !profileCalled {
		t.Fatal("storeProfileCredsFn was not called for named profile")
	}
}

func TestInitCmdProfileStoreError(t *testing.T) {
	dirs, key := setupCoverageState(t)
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")

	cfg := &config.Config{
		Vault: config.VaultConfig{
			VaultProfileConfig: config.VaultProfileConfig{KVVersion: 2},
			Profiles:           map[string]config.VaultProfileConfig{"work": {KVVersion: 2}},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/token/lookup-self":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"auth":{"lease_duration":0}}`)
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	if err := vlt.StoreCredentials(dirs, key, server.URL, "default-token"); err != nil {
		t.Fatal(err)
	}
	if err := vlt.StoreCredentialsForProfile(dirs, key, "work", server.URL, "work-token"); err != nil {
		t.Fatal(err)
	}

	origLoadCfg, origStoreDefault, origStoreProfile, origNewClient := loadConfigFn, storeCredentialsFn, storeProfileCredsFn, newClientFn
	defer func() {
		loadConfigFn = origLoadCfg
		storeCredentialsFn = origStoreDefault
		storeProfileCredsFn = origStoreProfile
		newClientFn = origNewClient
	}()
	loadConfigFn = func(string, string) (*config.Config, error) { return cfg, nil }
	storeCredentialsFn = func(_ *state.Dirs, _ []byte, _ string, _ string) error { return nil }
	storeProfileCredsFn = func(_ *state.Dirs, _ []byte, _ string, _ string, _ string) error {
		return errors.New("store-profile")
	}

	cmd := initCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "store-profile") {
		t.Fatalf("initCmd() error = %v, want store-profile error", err)
	}
}

func TestExecConfiguredCommandMissingProfile(t *testing.T) {
	setupCoverageState(t)

	cfg := &config.Config{
		Env: config.EnvConfig{Commands: []config.CommandEntry{{Name: "pi", Variables: []config.VariableEntry{{Name: "PI_API_KEY", Key: "api-key", Profile: "work"}}}}},
	}
	if err := execConfiguredCommand(cfg, "pi", []string{"--help"}, &state.Dirs{Shims: t.TempDir()}, nil); err == nil || !strings.Contains(err.Error(), "vault profile") {
		t.Fatalf("execConfiguredCommand() error = %v, want missing profile error", err)
	}
}

func TestSyncCmdMissingProfileError(t *testing.T) {
	setupCoverageState(t)

	origLoadCfg, origExpand := loadConfigFn, expandPathsFn
	defer func() {
		loadConfigFn = origLoadCfg
		expandPathsFn = origExpand
	}()
	loadConfigFn = func(string, string) (*config.Config, error) {
		return &config.Config{
			Files: []config.FileEntry{{Path: "/tmp/ignored", Key: "sync", Profile: "work"}},
		}, nil
	}
	expandPathsFn = func(*config.Config) error { return nil }

	cmd := syncCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "vault profile") {
		t.Fatalf("syncCmd() error = %v, want missing profile error", err)
	}
}

func TestStatusCmdShowsConfiguredProfiles(t *testing.T) {
	dirs, key := setupCoverageState(t)
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VSYNC_GLOBAL_CONFIG", "")
	t.Setenv("VSYNC_CONFIG", "")
	writeCoverageConfig(t, `vault:
  profiles:
    work:
      kv_version: 2
env:
  commands:
    - name: pi
      variables:
        - name: PI_API_KEY
          key: api-key
          profile: work
`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/token/lookup-self":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"auth":{"lease_duration":0}}`)
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	if err := vlt.StoreCredentials(dirs, key, server.URL, "default-token"); err != nil {
		t.Fatal(err)
	}

	origLoadCfg, origNewClient := loadConfigFn, newVaultClientFn
	defer func() {
		loadConfigFn = origLoadCfg
		newVaultClientFn = origNewClient
		flagKeyPath = ""
	}()
	loadConfigFn = func(string, string) (*config.Config, error) {
		return &config.Config{
			Vault: config.VaultConfig{
				VaultProfileConfig: config.VaultProfileConfig{KVVersion: 2},
				Profiles:           map[string]config.VaultProfileConfig{"work": {KVVersion: 2}},
			},
			Env: config.EnvConfig{Commands: []config.CommandEntry{{Name: "pi", Variables: []config.VariableEntry{{Name: "PI_API_KEY", Key: "api-key", Profile: "work"}}}}},
		}, nil
	}
	newVaultClientFn = func(creds *vlt.Credentials, kvVersion int) (*vlt.Client, error) {
		return vlt.NewClient(&vlt.Credentials{Addr: server.URL, Token: creds.Token}, kvVersion)
	}
	flagKeyPath = dirs.KeyFile()

	cmd := statusCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("statusCmd() error = %v", err)
	}
}
