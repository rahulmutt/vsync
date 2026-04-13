package celfilter

import (
	"context"
	"errors"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

func TestMatches(t *testing.T) {
	tests := []struct {
		name string
		expr string
		args []string
		want bool
	}{
		{
			name: "empty matches",
			expr: "",
			args: nil,
			want: true,
		},
		{
			name: "literal match",
			expr: `args.size() == 1 && args[0] == "--with-secrets"`,
			args: []string{"--with-secrets"},
			want: true,
		},
		{
			name: "literal mismatch",
			expr: `args.exists(a, a == "--with-secrets")`,
			args: []string{"--plain"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Matches(tt.expr, tt.args)
			if err != nil {
				t.Fatalf("Matches() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchesReportsInvalidExpression(t *testing.T) {
	if _, err := Matches(`args[`, []string{"x"}); err == nil {
		t.Fatal("Matches() error = nil, want compile error")
	}
}

func TestMatchesReportsNewEnvError(t *testing.T) {
	orig := newEnvFn
	defer func() { newEnvFn = orig }()
	newEnvFn = func(...cel.EnvOption) (celEnv, error) {
		return nil, errors.New("env")
	}

	if _, err := Matches("true", nil); err == nil || err.Error() != "create CEL environment: env" {
		t.Fatalf("Matches() error = %v, want create CEL environment error", err)
	}
}

func TestMatchesReportsProgramError(t *testing.T) {
	orig := newEnvFn
	defer func() { newEnvFn = orig }()
	newEnvFn = func(...cel.EnvOption) (celEnv, error) {
		return fakeEnv{programErr: errors.New("program")}, nil
	}

	if _, err := Matches("true", nil); err == nil || err.Error() != "build CEL program for \"true\": program" {
		t.Fatalf("Matches() error = %v, want program build error", err)
	}
}

func TestMatchesReportsEvalError(t *testing.T) {
	orig := newEnvFn
	defer func() { newEnvFn = orig }()
	newEnvFn = func(...cel.EnvOption) (celEnv, error) {
		return fakeEnv{evalErr: errors.New("eval")}, nil
	}

	if _, err := Matches("true", nil); err == nil || err.Error() != "evaluate CEL expression \"true\": eval" {
		t.Fatalf("Matches() error = %v, want eval error", err)
	}
}

func TestMatchesReportsNonBoolResult(t *testing.T) {
	if _, err := Matches("1", nil); err == nil || err.Error() != "CEL expression \"1\" returned int64, want bool" {
		t.Fatalf("Matches() error = %v, want non-bool result", err)
	}
}

type fakeEnv struct {
	programErr error
	evalErr    error
}

func (f fakeEnv) Compile(expr string) (*cel.Ast, *cel.Issues) {
	env, err := cel.NewEnv(cel.Variable("args", cel.ListType(cel.StringType)))
	if err != nil {
		panic(err)
	}
	ast, iss := env.Compile(expr)
	return ast, iss
}

func (f fakeEnv) Program(*cel.Ast, ...cel.ProgramOption) (cel.Program, error) {
	if f.programErr != nil {
		return nil, f.programErr
	}
	return fakeProgram{evalErr: f.evalErr}, nil
}

type fakeProgram struct {
	evalErr error
}

func (f fakeProgram) Eval(any) (ref.Val, *cel.EvalDetails, error) {
	if f.evalErr != nil {
		return nil, nil, f.evalErr
	}
	return types.Int(1), nil, nil
}

func (f fakeProgram) ContextEval(context.Context, any) (ref.Val, *cel.EvalDetails, error) {
	if f.evalErr != nil {
		return nil, nil, f.evalErr
	}
	return types.Int(1), nil, nil
}
