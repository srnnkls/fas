package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/ast"

	"github.com/srnnkls/quae/internal/config"
)

// TestLoadRules_WhenSyntax_Populated pins the contract that decoding a rule
// populates Rule.WhenSyntax with a non-nil ast.Expr carrying source positions
// from the original .cue file.
func TestLoadRules_WhenSyntax_Populated(t *testing.T) {
	const src = `package rules

simple_rule: {
	when: {tool_name: "Bash"}
	then: deny: {
		rule_id: "r1"
		reason:  "nope"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "simple.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	if rules[0].WhenSyntax == nil {
		t.Fatal("expected Rule.WhenSyntax to be non-nil after loading")
	}

	// Position must be valid — a nil-typed pointer masquerading as an
	// interface value would pass the non-nil check but yield an invalid
	// position.
	if !rules[0].WhenSyntax.Pos().IsValid() {
		t.Fatal("expected Rule.WhenSyntax.Pos() to be valid")
	}
}

// TestLoadRules_WhenSyntax_ResolvesSourcePosition anchors on precise line
// numbers in the fixture so the assertion fails loudly if WhenSyntax drifts
// away from the true source span. Layout (1-indexed):
//
//	line 1 — `package rules`
//	line 2 — blank
//	line 3 — `positioned_rule: {`
//	line 4 — `	when: {hook_event_name: "PreToolUse"}`
//
// The `when` value is the struct literal on line 4.
func TestLoadRules_WhenSyntax_ResolvesSourcePosition(t *testing.T) {
	const src = `package rules

positioned_rule: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r1"
		reason:  "nope"
	}
}
`
	dir := t.TempDir()
	rulePath := writeRuleFile(t, dir, "positioned.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	pos := rules[0].WhenSyntax.Pos()
	if !pos.IsValid() {
		t.Fatal("WhenSyntax.Pos must be valid")
	}

	// Filename in the CUE token is the virtual overlay name, not the real
	// rule path. Authors see the real path in the rendered diagnostic; the
	// loader is responsible for threading it in via ruleLoadError. For the
	// AST-level assertion we check that the filename at minimum references
	// the base name (CUE's loader may rewrite the virtual prefix).
	base := filepath.Base(rulePath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	filename := pos.Filename()
	if filename == "" {
		t.Fatal("WhenSyntax.Pos has empty filename")
	}
	if !strings.Contains(filename, stem) {
		t.Fatalf("WhenSyntax.Pos filename = %q, want a reference to rule stem %q",
			filename, stem)
	}

	// `when: {...}` sits on line 4. The Pos points at the value expression —
	// the `{` of the struct literal.
	const wantLine = 4
	if pos.Line() != wantLine {
		t.Fatalf("WhenSyntax.Pos.Line = %d, want %d (value expression of `when:` on line %d)",
			pos.Line(), wantLine, wantLine)
	}
}

// TestLoadRules_WhenSyntax_NestedFieldPositions anchors on a nested AST node —
// `command: =~"^rm "` lives inside `tool_input: {...}`. The test walks the
// StructLit/Field tree and asserts the inner `command` label sits on the
// expected line. Layout:
//
//	line 1 — `package rules`
//	line 2 — blank
//	line 3 — `nested_rule: {`
//	line 4 — `	when: {`
//	line 5 — `		tool_input: {`
//	line 6 — `			command: =~"^rm "`
//	line 7 — `		}`
func TestLoadRules_WhenSyntax_NestedFieldPositions(t *testing.T) {
	const src = `package rules

nested_rule: {
	when: {
		tool_input: {
			command: =~"^rm "
		}
	}
	then: deny: {
		rule_id: "r1"
		reason:  "nope"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "nested.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	commandField := findFieldByName(t, rules[0].WhenSyntax, []string{"tool_input", "command"})
	if commandField == nil {
		t.Fatal("failed to find nested `command` field in WhenSyntax AST")
	}

	labelPos := commandField.Label.Pos()
	if !labelPos.IsValid() {
		t.Fatal("`command` label has invalid position")
	}
	const wantLine = 6
	if labelPos.Line() != wantLine {
		t.Fatalf("`command` label line = %d, want %d", labelPos.Line(), wantLine)
	}
}

// TestLoadRules_WhenSyntax_PerRuleIsolation loads a multi-rule file and asserts
// each rule's WhenSyntax is a distinct AST node with its own positions. This
// guards the case where a single shared AST node could accidentally be pointed
// to from multiple rules (aliasing bug).
func TestLoadRules_WhenSyntax_PerRuleIsolation(t *testing.T) {
	// Fixture layout:
	//   line 1  — `package rules`
	//   line 2  — blank
	//   line 3  — `rule_alpha: {`
	//   line 4  — `	when: {tool_name: "Bash"}`
	//   ...
	//   line 10 — `rule_beta: {`
	//   line 11 — `	when: {tool_name: "Write"}`
	const src = `package rules

rule_alpha: {
	when: {tool_name: "Bash"}
	then: deny: {
		rule_id: "alpha"
		reason:  "nope"
	}
}
rule_beta: {
	when: {tool_name: "Write"}
	then: deny: {
		rule_id: "beta"
		reason:  "nope"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "multi.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	aSyntax := rules[0].WhenSyntax
	bSyntax := rules[1].WhenSyntax
	if aSyntax == nil || bSyntax == nil {
		t.Fatalf("both rules must have non-nil WhenSyntax; got alpha=%v beta=%v",
			aSyntax, bSyntax)
	}

	// Distinct AST nodes — the underlying pointers must differ. Comparing
	// interface values compares (type, pointer); two distinct StructLits
	// allocated by the parser compare unequal.
	if aSyntax == bSyntax {
		t.Fatal("per-rule WhenSyntax nodes must be distinct, got the same AST node reference")
	}

	aLine := aSyntax.Pos().Line()
	bLine := bSyntax.Pos().Line()
	if aLine == bLine {
		t.Fatalf("per-rule WhenSyntax positions must differ, both on line %d", aLine)
	}
	// Anchor to the fixture: alpha's `when` is on line 4, beta's on line 11.
	if aLine != 4 {
		t.Errorf("rule_alpha WhenSyntax line = %d, want 4", aLine)
	}
	if bLine != 11 {
		t.Errorf("rule_beta WhenSyntax line = %d, want 11", bLine)
	}
}

// findFieldByName walks a StructLit AST following a path of field names and
// returns the terminal *ast.Field, or nil if any segment is missing / the node
// shape does not match the expected struct-of-structs layout.
func findFieldByName(t *testing.T, node ast.Expr, path []string) *ast.Field {
	t.Helper()
	if len(path) == 0 {
		return nil
	}
	st, ok := node.(*ast.StructLit)
	if !ok {
		t.Fatalf("expected top-level WhenSyntax to be *ast.StructLit, got %T", node)
	}
	current := st
	var last *ast.Field
	for i, segment := range path {
		last = nil
		for _, decl := range current.Elts {
			f, ok := decl.(*ast.Field)
			if !ok {
				continue
			}
			if labelName(f.Label) != segment {
				continue
			}
			last = f
			if i == len(path)-1 {
				return f
			}
			inner, ok := f.Value.(*ast.StructLit)
			if !ok {
				t.Fatalf("path %s: expected *ast.StructLit at segment %q, got %T",
					joinSegments(path[:i+1]), segment, f.Value)
			}
			current = inner
			break
		}
		if last == nil {
			return nil
		}
	}
	return last
}

// labelName returns the textual name of a field label, supporting Ident and
// BasicLit string labels. Unknown label kinds return the empty string so the
// walker skips them.
func labelName(l ast.Label) string {
	switch v := l.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.BasicLit:
		// String labels arrive as quoted literals; strip quotes.
		s := v.Value
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			return s[1 : len(s)-1]
		}
		return s
	}
	return ""
}

// joinSegments formats a path for error messages.
func joinSegments(segments []string) string {
	return strings.Join(segments, ".")
}
