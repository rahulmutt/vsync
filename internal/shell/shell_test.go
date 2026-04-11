package shell

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

	t.Setenv("PATH", "")
	if got := buildEnv("/tmp/shims", "/tmp/key"); !strings.Contains(strings.Join(got, "\n"), "PATH=/tmp/shims") {
		t.Fatalf("PATH not set when PATH empty: %v", got)
	}
}

func TestLaunchRejectsNestedShellAndMissingBinary(t *testing.T) {
	t.Setenv("VSYNC_ACTIVE", "1")
	if err := Launch("/bin/sh", "/tmp/shims", "/tmp/key"); err == nil {
		t.Fatal("Launch() error = nil, want nested shell error")
	}
	t.Setenv("VSYNC_ACTIVE", "")
	if err := Launch("/definitely/not-a-shell", "/tmp/shims", "/tmp/key"); err == nil {
		t.Fatal("Launch() error = nil, want shell not found error")
	}
}

func TestLaunchBuildsEnvAndCallsExec(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	oldLookPath, oldExec := lookPathFn, syscallExecFn
	defer func() {
		lookPathFn = oldLookPath
		syscallExecFn = oldExec
	}()

	lookPathFn = func(file string) (string, error) {
		if file != "sh" {
			t.Fatalf("LookPath file = %q, want sh", file)
		}
		return "/bin/sh", nil
	}
	var gotPath string
	var gotArgv []string
	var gotEnv []string
	syscallExecFn = func(path string, argv []string, env []string) error {
		gotPath = path
		gotArgv = append([]string(nil), argv...)
		gotEnv = append([]string(nil), env...)
		return errors.New("exec stub")
	}

	if err := Launch("sh", "/tmp/shims", "/tmp/key"); err == nil || err.Error() != "exec stub" {
		t.Fatalf("Launch() error = %v, want exec stub", err)
	}
	if gotPath != "/bin/sh" {
		t.Fatalf("exec path = %q, want /bin/sh", gotPath)
	}
	if !reflect.DeepEqual(gotArgv, []string{"sh"}) {
		t.Fatalf("argv = %#v, want [sh]", gotArgv)
	}
	joined := strings.Join(gotEnv, "\n")
	if !strings.Contains(joined, "PATH=/tmp/shims"+string(filepath.ListSeparator)+"/usr/bin:/bin") {
		t.Fatalf("PATH not prepended: %v", gotEnv)
	}
	if !strings.Contains(joined, "VSYNC_ACTIVE=1") || !strings.Contains(joined, "VSYNC_KEY=/tmp/key") {
		t.Fatalf("env missing vsync vars: %v", gotEnv)
	}
}

func TestExecCommandMissingBinaryAndMergesEnv(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	oldExec := syscallExecFn
	defer func() { syscallExecFn = oldExec }()

	var gotPath string
	var gotArgv []string
	var gotEnv []string
	syscallExecFn = func(path string, argv []string, env []string) error {
		gotPath = path
		gotArgv = append([]string(nil), argv...)
		gotEnv = append([]string(nil), env...)
		return errors.New("exec stub")
	}
	if err := ExecCommand("ls", []string{"-l"}, map[string]string{"A": "1", "B": "2"}, "/tmp/shims"); err == nil || err.Error() != "exec stub" {
		t.Fatalf("ExecCommand() error = %v, want exec stub", err)
	}
	if gotPath != "/usr/bin/ls" {
		t.Fatalf("exec path = %q, want /usr/bin/ls", gotPath)
	}
	if !reflect.DeepEqual(gotArgv, []string{"ls", "-l"}) {
		t.Fatalf("argv = %#v, want [ls -l]", gotArgv)
	}
	joined := strings.Join(gotEnv, "\n")
	if !strings.Contains(joined, "A=1") || !strings.Contains(joined, "B=2") {
		t.Fatalf("extra env not merged: %v", gotEnv)
	}

	t.Setenv("PATH", "")
	if _, err := findReal("missing", "/tmp/shims"); err == nil {
		t.Fatal("findReal() error = nil, want command not found")
	}
	noExec := filepath.Join(t.TempDir(), "noexec")
	if err := os.WriteFile(noExec, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", noExec)
	if _, err := findReal("noexec", "/tmp/shims"); err == nil {
		t.Fatal("findReal() error = nil, want not executable error")
	}
	if err := ExecCommand("missing", nil, nil, "/tmp/shims"); err == nil {
		t.Fatal("ExecCommand() error = nil, want command not found")
	}
}

func TestFindRealSkipsShimDirAndErrors(t *testing.T) {
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

	t.Setenv("PATH", "")
	if _, err := findReal("missing", shimDir); err == nil {
		t.Fatal("findReal() error = nil, want command not found")
	}
}
