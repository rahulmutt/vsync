// Package celfilter evaluates CEL expressions used to gate secret injection.
package celfilter

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
)

// Matches evaluates expr against the provided command args.
//
// The CEL environment exposes a single variable:
//   - args: list<string>
//
// An empty expression matches by default.
func Matches(expr string, args []string) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true, nil
	}

	env, err := cel.NewEnv(cel.Variable("args", cel.ListType(cel.StringType)))
	if err != nil {
		return false, fmt.Errorf("create CEL environment: %w", err)
	}

	ast, iss := env.Compile(expr)
	if iss.Err() != nil {
		return false, fmt.Errorf("compile CEL expression %q: %w", expr, iss.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return false, fmt.Errorf("build CEL program for %q: %w", expr, err)
	}

	out, _, err := prg.Eval(map[string]any{"args": args})
	if err != nil {
		return false, fmt.Errorf("evaluate CEL expression %q: %w", expr, err)
	}

	matched, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression %q returned %T, want bool", expr, out.Value())
	}
	return matched, nil
}
