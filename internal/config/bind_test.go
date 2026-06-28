package config

import (
	"testing"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"
)

func TestParseBindAttr_Valid(t *testing.T) {
	tests := []struct {
		text     string
		variable string
		subPath  string
	}{
		{"@bind(X)", "X", ""},
		{"@bind(Cmd)", "Cmd", ""},
		{"@bind(X,0)", "X", "0"},
		{"@bind(X, 0)", "X", "0"},
		{"@bind(Target, 1)", "Target", "1"},
		{"@bind(Val,foo)", "Val", "foo"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			v, sp, err := parseBindAttr(tt.text)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v != tt.variable {
				t.Errorf("variable = %q, want %q", v, tt.variable)
			}
			if sp != tt.subPath {
				t.Errorf("subPath = %q, want %q", sp, tt.subPath)
			}
		})
	}
}

func TestParseBindAttr_NonBind(t *testing.T) {
	v, sp, err := parseBindAttr("@json(name)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "" || sp != "" {
		t.Errorf("non-@bind attr should return empty; got variable=%q, subPath=%q", v, sp)
	}
}

func TestParseBindAttr_Invalid(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"@bind()", "requires a variable name"},
		{"@bind(x)", "uppercase"},
		{"@bind(123)", "uppercase"},
		{"@bind(X,)", "sub-path is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			_, _, err := parseBindAttr(tt.text)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.want)
			}
		})
	}
}

func TestExtractBindings_SimpleEquality(t *testing.T) {
	src := `{
		tool_input: {
			command: string @bind(X)
			parsed: {
				target: string @bind(X)
			}
		}
	}`
	f, err := parser.ParseFile("test.cue", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The file has one top-level expression (the struct).
	expr := fileExpr(t, f)

	bindings, err := extractBindings(expr)
	if err != nil {
		t.Fatalf("extractBindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(bindings))
	}

	b0 := bindings[0]
	if b0.Variable != "X" {
		t.Errorf("binding[0].Variable = %q, want %q", b0.Variable, "X")
	}
	wantPath0 := []string{"tool_input", "command"}
	if !pathEqual(b0.FieldPath, wantPath0) {
		t.Errorf("binding[0].FieldPath = %v, want %v", b0.FieldPath, wantPath0)
	}

	b1 := bindings[1]
	if b1.Variable != "X" {
		t.Errorf("binding[1].Variable = %q, want %q", b1.Variable, "X")
	}
	wantPath1 := []string{"tool_input", "parsed", "target"}
	if !pathEqual(b1.FieldPath, wantPath1) {
		t.Errorf("binding[1].FieldPath = %v, want %v", b1.FieldPath, wantPath1)
	}
}

func TestExtractBindings_WithSubPath(t *testing.T) {
	src := `{
		command: string @bind(X)
		targets: [...string] @bind(X, 0)
	}`
	f, err := parser.ParseFile("test.cue", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	expr := fileExpr(t, f)

	bindings, err := extractBindings(expr)
	if err != nil {
		t.Fatalf("extractBindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(bindings))
	}

	if bindings[1].SubPath != "0" {
		t.Errorf("binding[1].SubPath = %q, want %q", bindings[1].SubPath, "0")
	}
	if bindings[1].SubIndex() != 0 {
		t.Errorf("binding[1].SubIndex() = %d, want 0", bindings[1].SubIndex())
	}
}

func TestExtractBindings_NoBindings(t *testing.T) {
	src := `{
		tool_name: "Bash"
		tool_input: command: =~"^rm"
	}`
	f, err := parser.ParseFile("test.cue", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	expr := fileExpr(t, f)

	bindings, err := extractBindings(expr)
	if err != nil {
		t.Fatalf("extractBindings: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected 0 bindings, got %d", len(bindings))
	}
}

func TestExtractBindings_MultipleVariables(t *testing.T) {
	src := `{
		a: string @bind(X)
		b: string @bind(X)
		c: string @bind(Y)
		d: string @bind(Y)
	}`
	f, err := parser.ParseFile("test.cue", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	expr := fileExpr(t, f)

	bindings, err := extractBindings(expr)
	if err != nil {
		t.Fatalf("extractBindings: %v", err)
	}
	if len(bindings) != 4 {
		t.Fatalf("expected 4 bindings, got %d", len(bindings))
	}

	xCount, yCount := 0, 0
	for _, b := range bindings {
		switch b.Variable {
		case "X":
			xCount++
		case "Y":
			yCount++
		}
	}
	if xCount != 2 {
		t.Errorf("X bindings = %d, want 2", xCount)
	}
	if yCount != 2 {
		t.Errorf("Y bindings = %d, want 2", yCount)
	}
}

// fileExpr extracts the struct literal from a parsed CUE file whose top-level
// content is a single struct expression wrapped in an EmbedDecl.
func fileExpr(t *testing.T, f *ast.File) ast.Expr {
	t.Helper()
	if len(f.Decls) == 1 {
		if embed, ok := f.Decls[0].(*ast.EmbedDecl); ok {
			return embed.Expr
		}
	}
	t.Fatal("expected a single EmbedDecl wrapping a StructLit")
	return nil
}

func pathEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
