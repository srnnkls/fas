package evaluator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/token"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
	"github.com/srnnkls/fas/internal/evaluator"
)

// -----------------------------------------------------------------------------
// Localize walker tests (T6) — E0201 (absent path), E0301 (leaf constraint),
// E0401 (disjunction all-fail), optional-field no-diag, caller-break halt.
//
// These tests run with the package-level explain toggle ON. They assert the
// structured shape of a Diagnostic (code, caret position, want/got labels,
// arm labels) rather than the rendered string — the renderer is exercised
// elsewhere.
// -----------------------------------------------------------------------------

// writeLocalizeRule drops a rule fixture into dir and returns the absolute path
// so tests can correlate caret positions with the on-disk source.
func writeLocalizeRule(t *testing.T, dir, name, body string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("package rules\n\n")
	b.WriteString("rule: ")
	b.WriteString(body)
	b.WriteString("\n")
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write rule: %v", err)
	}
	return p
}

func loadOne(t *testing.T, dir string) config.Rule {
	t.Helper()
	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule loaded, got %d", len(rules))
	}
	return rules[0]
}

func compileVal(t *testing.T, src string) cue.Value {
	t.Helper()
	ctx := cuecontext.New()
	v := ctx.CompileString(src, cue.Filename("input.cue"))
	if err := v.Err(); err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return v
}

// findDiagWithCode returns the first diagnostic carrying code, or fails the
// test. It makes the per-case assertions read as intent, not plumbing.
func findDiagWithCode(t *testing.T, diags []diag.Diagnostic, code string) diag.Diagnostic {
	t.Helper()
	for _, d := range diags {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("no diagnostic with code %s found; got %d diagnostics: %+v", code, len(diags), diags)
	return diag.Diagnostic{}
}

// -----------------------------------------------------------------------------
// E0201 — absent path segment
// -----------------------------------------------------------------------------

// TestLocalize_E0201_AbsentPathSegment_CaretsOnFieldLabel verifies that when
// the rule requires nested path a.b.c and the input supplies only `a`, the
// walker yields E0201 at the label position of the first absent segment
// (`b`). Two independent anchors pin the caret:
//
//  1. AST equality — d.Primary.Pos equals the ast.Field.Label.Pos() we
//     extract from the retained WhenSyntax. This proves the walker is
//     operating on the same AST the renderer will.
//  2. file:line:col resolution — token.Pos.Position() resolves through CUE's
//     internal FileSet to a human-readable coordinate. We assert the filename
//     references the fixture stem, the line matches where `b:` was written,
//     and the column points at the `b` identifier (not start-of-line).
//
// The fixture places each field on its own line with tab indentation so the
// `b:` position is stable: body line 1 is `{`, body line 2 is `\twhen: {`,
// body line 3 is `\t\ta: {`, body line 4 is `\t\t\tb: c: true`. In the
// assembled file (package header + `rule: ` prefix), `b:` lands on line 6.
// The column is the byte offset of `b` within its line (3 tabs + 1 = col 4).
func TestLocalize_E0201_AbsentPathSegment_CaretsOnFieldLabel(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	// Fixture layout (1-indexed, tabs preserved):
	//   line 1 — `package rules`
	//   line 2 — blank
	//   line 3 — `rule: {`
	//   line 4 — `\twhen: {`
	//   line 5 — `\t\ta: {`
	//   line 6 — `\t\t\tb: c: true`
	//   line 7 — `\t\t}`
	//   line 8 — `\t}`
	//   line 9 — `\tthen: deny: {rule_id: "r", reason: "nope"}`
	//  line 10 — `}`
	//
	// `b:` sits on line 6, column 4 (three tabs then `b`).
	body := "{\n" +
		"\twhen: {\n" +
		"\t\ta: {\n" +
		"\t\t\tb: c: true\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\tthen: deny: {rule_id: \"r\", reason: \"nope\"}\n" +
		"}"
	dir := t.TempDir()
	rulePath := writeLocalizeRule(t, dir, "nested.cue", body)
	rule := loadOne(t, dir)

	// Anchor 1: pos equality against the retained AST node the renderer will
	// use. Walking the AST with the same accessor the walker uses is a
	// tautology, but it's a structural check that proves the walker found
	// the same AST node — not that the position resolves correctly.
	wantPos := labelPosForPath(t, rule.WhenSyntax, []string{"a", "b"})

	// Input provides `a` but not `a.b`.
	input := compileVal(t, `{a: {other: 1}}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiagWithCode(t, diags, "E0201")

	if d.Primary.Pos != wantPos {
		t.Fatalf("E0201 primary caret pos = %v, want label pos %v", d.Primary.Pos, wantPos)
	}
	if d.Severity != diag.SeverityError {
		t.Fatalf("E0201 severity = %v, want SeverityError", d.Severity)
	}
	if d.Help == "" {
		t.Error("E0201 must carry a Help listing available keys at the parent")
	}
	if !strings.Contains(d.Help, "other") {
		t.Errorf("E0201 Help should list actual input keys (expected `other`); got %q", d.Help)
	}

	// Anchor 2: file:line:col resolution via token.Pos.Position(). CUE's
	// loader uses a virtual overlay path (see whensyntax_test.go), so the
	// filename is checked for the fixture stem rather than equality.
	if !d.Primary.Pos.IsValid() {
		t.Fatalf("E0201 primary pos must be valid; got %v", d.Primary.Pos)
	}
	gotFilename := d.Primary.Pos.Filename()
	stem := strings.TrimSuffix(filepath.Base(rulePath), filepath.Ext(rulePath))
	if gotFilename == "" {
		t.Fatal("E0201 primary pos has empty filename")
	}
	if !strings.Contains(gotFilename, stem) {
		t.Errorf("E0201 primary pos filename = %q, want a reference to stem %q",
			gotFilename, stem)
	}
	const wantLine = 6
	if got := d.Primary.Pos.Line(); got != wantLine {
		t.Errorf("E0201 primary pos line = %d, want %d (the line `b:` was written on)",
			got, wantLine)
	}
	const wantColumn = 4 // three tabs (\t\t\t) then `b`
	if got := d.Primary.Pos.Column(); got != wantColumn {
		t.Errorf("E0201 primary pos column = %d, want %d (column of the `b` identifier)",
			got, wantColumn)
	}
}

// labelPosForPath walks a when AST following a chain of field names and
// returns the token.Pos of the final field's Label. Failure aborts the test.
func labelPosForPath(t *testing.T, expr ast.Expr, path []string) token.Pos {
	t.Helper()
	st, ok := expr.(*ast.StructLit)
	if !ok {
		t.Fatalf("when syntax root is %T, want *ast.StructLit", expr)
	}
	cur := st
	for i, name := range path {
		found := false
		for _, decl := range cur.Elts {
			f, isField := decl.(*ast.Field)
			if !isField {
				continue
			}
			if fieldLabelName(f.Label) != name {
				continue
			}
			found = true
			if i == len(path)-1 {
				return f.Label.Pos()
			}
			inner, ok := f.Value.(*ast.StructLit)
			if !ok {
				t.Fatalf("path %q value is %T, want *ast.StructLit", name, f.Value)
			}
			cur = inner
			break
		}
		if !found {
			t.Fatalf("label %q not found in when AST at depth %d", name, i)
		}
	}
	t.Fatal("unreachable")
	return token.NoPos
}

// fieldLabelName extracts the field name from an ast.Label, supporting both
// Ident and BasicLit (quoted) forms — matches what the walker will see.
func fieldLabelName(l ast.Label) string {
	switch x := l.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.BasicLit:
		return strings.Trim(x.Value, `"`)
	}
	return ""
}

// TestLocalize_E0201_OptionalField_NoDiagnostic locks in that absent paths
// under an optional field do NOT emit a diagnostic — optional semantics mean
// "may be present or absent", and an absent optional is a match, not a miss.
// This guards against the common walker bug of treating every absent path as
// an error regardless of optionality.
func TestLocalize_E0201_OptionalField_NoDiagnostic(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "opt.cue", `{
		when: {flags?: {force?: !=true}}
		then: deny: {rule_id: "r", reason: "nope"}
	}`)
	rule := loadOne(t, dir)

	// Input omits `flags` entirely. Because the field is optional, Subsume
	// succeeds — the rule MATCHES. Consequently the walker must not run,
	// and there must be no diagnostic either way.
	input := compileVal(t, `{hook_event_name: "PreToolUse"}`)

	matches, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("optional-field rule must match when field absent; got %d matches", len(matches))
	}
	if len(diags) != 0 {
		t.Fatalf("optional-field absent: no diagnostic expected, got %d: %+v", len(diags), diags)
	}
}

// -----------------------------------------------------------------------------
// E0301 — leaf constraint failure (regex / type / range)
// -----------------------------------------------------------------------------

// TestLocalize_E0301_RegexLeafFails_NoLegacyWantGot verifies the T11 spec
// change (F7 "no-restate"): a literal regex `command: =~"^rm "` whose
// formatted source matches the expanded ruleNext form must NOT emit the
// legacy `want:` / `got:` Notes — the caret row already underlines the
// pattern, and the new Reasons (T4-T7) carry the input via RegexMismatch.
// Primary.Msg must also be empty (Title "leaf constraint failed" carries
// that signal already).
//
// Legacy spec change: predecessor of this test
// (TestLocalize_E0301_RegexLeafFails_WantGotLabels) pinned the opposite
// behavior — the old renderer depended on both Notes. T11 supersedes it.
func TestLocalize_E0301_RegexLeafFails_NoLegacyWantGot(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "rm_leaf.cue", `{
		when: {command: =~"^rm "}
		then: deny: {rule_id: "r", reason: "nope"}
	}`)
	rule := loadOne(t, dir)

	input := compileVal(t, `{command: "ls -la"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiagWithCode(t, diags, "E0301")

	if !d.Primary.Pos.IsValid() {
		t.Fatalf("E0301 primary pos must be valid; got %v", d.Primary.Pos)
	}
	if d.Primary.Msg != "" {
		t.Errorf("E0301 Primary.Msg = %q, want empty (Title carries the restatement)",
			d.Primary.Msg)
	}
	for _, n := range d.Notes {
		low := strings.ToLower(n.Msg)
		if strings.Contains(low, "want:") {
			t.Errorf("E0301 literal regex must NOT emit `want:` Note; got %q", n.Msg)
		}
		if strings.Contains(low, "got:") {
			t.Errorf("E0301 literal regex must NOT emit legacy `got:` Note; got %q", n.Msg)
		}
	}
}

// -----------------------------------------------------------------------------
// Iterator semantics — caller break halts the walker cleanly
// -----------------------------------------------------------------------------

// TestLocalize_CallerBreak_HaltsWalker verifies that a caller who breaks out
// of `for d := range localize(rule, input)` after the first diagnostic stops
// the walker immediately — the iterator honors the yield-false protocol.
//
// The test uses Exported since localize is unexported. We rely on the
// package-internal surface via an exported shim — if no such shim exists yet,
// this test compiles but fails because the shim is nil.
func TestLocalize_CallerBreak_HaltsWalker(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	// Two independent absent segments guarantee >1 diagnostic if the walker
	// runs to completion; after break we must observe exactly 1.
	writeLocalizeRule(t, dir, "two_absent.cue", `{
		when: {a: true, b: true}
		then: deny: {rule_id: "r", reason: "nope"}
	}`)
	rule := loadOne(t, dir)

	// Input has neither `a` nor `b` — walker would yield two diagnostics if
	// allowed to continue.
	input := compileVal(t, `{other: 1}`)

	seq := evaluator.LocalizeForTest(rule, input)
	count := 0
	for range seq {
		count++
		break
	}
	if count != 1 {
		t.Fatalf("walker must honor break after first yield; saw %d diagnostics", count)
	}
}

// TestLocalize_NoBreak_YieldsAll is the sibling of
// TestLocalize_CallerBreak_HaltsWalker: same fixture, same input, but the
// caller does NOT break. The walker must yield every diagnostic it discovers.
// The pair pins break semantics — a buggy walker that short-circuits after
// one yield would pass the break test trivially; this companion catches
// that failure mode by requiring >=2 diagnostics when the caller lets the
// iterator run to completion.
func TestLocalize_NoBreak_YieldsAll(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	// Same two-absent-sibling shape as the break test — two independent
	// E0201 sites on sibling fields.
	writeLocalizeRule(t, dir, "two_absent.cue", `{
		when: {a: true, b: true}
		then: deny: {rule_id: "r", reason: "nope"}
	}`)
	rule := loadOne(t, dir)

	// Input has neither `a` nor `b` — a complete walk must surface two
	// diagnostics (one per absent sibling).
	input := compileVal(t, `{other: 1}`)

	seq := evaluator.LocalizeForTest(rule, input)
	count := 0
	for range seq {
		count++
	}
	if count < 2 {
		t.Fatalf("no-break run must yield every diagnostic; saw %d, want >=2", count)
	}
}
