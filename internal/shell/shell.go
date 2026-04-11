// Package shell builds the augmented environment and execs into a shell.
package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Launch execs into the given shell binary with the augmented environment.
// shimDir is prepended to PATH; keyFile path is exported as VSYNC_KEY.
func Launch(shellBin, shimDir, keyFile string) error {
	if os.Getenv("VSYNC_ACTIVE") == "1" {
		return fmt.Errorf("already inside a vsync shell (VSYNC_ACTIVE=1); nested shells are not supported")
	}

	env := buildEnv(shimDir, keyFile)
	argv := []string{shellBin}

	shellPath, err := exec.LookPath(shellBin)
	if err != nil {
		return fmt.Errorf("shell not found: %s: %w", shellBin, err)
	}

	return syscall.Exec(shellPath, argv, env)
}

// ExecCommand finds the real binary for name (skipping shimDir), then syscall.Exec's it
// with the provided env overlaid on os.Environ().
func ExecCommand(name string, args []string, extraEnv map[string]string, shimDir string) error {
	realPath, err := findReal(name, shimDir)
	if err != nil {
		return err
	}

	env := os.Environ()
	for k, v := range extraEnv {
		env = setEnvVar(env, k, v)
	}

	argv := append([]string{name}, args...)
	return syscall.Exec(realPath, argv, env)
}

// findReal locates the binary for name in PATH, skipping shimDir.
func findReal(name, shimDir string) (string, error) {
	shimDir = filepath.Clean(shimDir)
	pathEnv := os.Getenv("PATH")
	dirs := filepath.SplitList(pathEnv)

	for _, dir := range dirs {
		if filepath.Clean(dir) == shimDir {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("command not found: %s (searched PATH excluding shims)", name)
}

// buildEnv builds the child environment.
func buildEnv(shimDir, keyFile string) []string {
	env := os.Environ()
	// Prepend shims to PATH.
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = shimDir
	} else {
		pathEnv = shimDir + string(filepath.ListSeparator) + pathEnv
	}
	env = setEnvVar(env, "PATH", pathEnv)
	env = setEnvVar(env, "VSYNC_ACTIVE", "1")
	env = setEnvVar(env, "VSYNC_KEY", keyFile)
	return env
}

// setEnvVar sets or replaces KEY=value in an env slice.
func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
