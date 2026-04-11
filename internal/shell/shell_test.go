package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetEnvVar(t *testing.T) {
	env := []string{"A=1", "B=2"}
	env = setEnvVar(env, "B", "3")
	env = setEnvVar(env, "C", "4")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "B=3") || !strings.Contains(joined, "C=4") {
		t.Fatalf("setEnvVar() = %v", env)
	}
}

func TestBuildEnvPrependsShimDirAndSetsVsyncVars(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	env := buildEnv("/tmp/shims", "/tmp/key")
	got := strings.Join(env, "\n")
	if !strings.Contains(got, "PATH=/tmp/shims"+string(filepath.ListSeparator)+"/usr/bin:/bin") {
		t.Fatalf("PATH not prepended: %v", env)
	}
	if !strings.Contains(got, "VSYNC_ACTIVE=1") {
		t.Fatalf("VSYNC_ACTIVE not set: %v", env)
	}
	if !strings.Contains(got, "VSYNC_KEY=/tmp/key") {
		t.Fatalf("VSYNC_KEY not set: %v", env)
	}
}

func TestFindRealSkipsShimDir(t *testing.T) {
	root := t.TempDir()
	shimDir := filepath.Join(root, "shims")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(shimDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	shimPath := filepath.Join(shimDir, "tool")
	realPath := filepath.Join(binDir, "tool")
	if err := os.WriteFile(shimPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(filepath.ListSeparator)+binDir)
	got, err := findReal("tool", shimDir)
	if err != nil {
		t.Fatalf("findReal() error = %v", err)
	}
	if got != realPath {
		t.Fatalf("findReal() = %q, want %q", got, realPath)
	}
}
