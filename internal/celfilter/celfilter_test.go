package celfilter

import "testing"

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
