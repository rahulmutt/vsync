package main

import (
	"bytes"
	"context"
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
		flagConfigPath = ""
		flagVaultAddr = ""
		flagVaultToken = ""
		flagKeyPath = ""
		defaultDirsFn = state.DefaultDirs
		generateKeyFn = crypto.GenerateKey
		loadOrGenerateKeyFn = crypto.LoadOrGenerateKey
		loadKeyFn = crypto.LoadKey
		storeCredentialsFn = vlt.StoreCredentials
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
