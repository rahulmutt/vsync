// Package shim generates and manages shim scripts in ~/.local/state/vsync/shims/.
package shim

import (
	"fmt"
	"os"

	"github.com/vsync/vsync/internal/state"
)

// shimScript is the template for each shim. It delegates to `vsync exec <name>`.
const shimScript = `#!/bin/sh
exec vsync exec "%s" "$@"
`

// Ensure creates or updates shim scripts for the given command names.
// Existing shims are overwritten unconditionally.
func Ensure(dirs *state.Dirs, commands []string) error {
	for _, name := range commands {
		if err := write(dirs, name); err != nil {
			return err
		}
	}
	return nil
}

func write(dirs *state.Dirs, name string) error {
	path := dirs.ShimFile(name)
	content := fmt.Sprintf(shimScript, name)
	if err := state.WriteAtomic(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("write shim %s: %w", name, err)
	}
	return nil
}

// Remove deletes a shim script.
func Remove(dirs *state.Dirs, name string) error {
	path := dirs.ShimFile(name)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// List returns the names of all existing shims.
func List(dirs *state.Dirs) ([]string, error) {
	entries, err := os.ReadDir(dirs.Shims)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
