package evaluator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/quae/internal/config"
	"github.com/srnnkls/quae/internal/diag"
	"github.com/srnnkls/quae/internal/evaluator"
)

// -----------------------------------------------------------------------------
// T11 — No-restate rule (decision at emission time in localize.go)
//
// Per feedback_diag_no_restate.md and scope.md F7:
//   1. Drop Primary `Msg` when it duplicates the Title — "constraint not
//      satisfied" is already carried by Title "leaf constraint failed".
//   2. Drop the legacy "got:" Note — the new Reasons (T4-T7) carry that info.
//   3. Conditional `want:` label. Emit `want:` only when EITHER:
//        - f.Value is *ast.Ident or *ast.SelectorExpr (cheap AST gate — it's a
//          reference like #DangerousCmds; expanded form helps the reader).
//        - formatted(ruleNext.Expr()) != formatted(f.Value) (format-divergence
//          gate — unification narrowed the constraint; show the expanded form).
//
// Tests reuse writeKindFile / loadOneRule / compileValKind / findDiag helpers
// from localize_kind_test.go.
// -----------------------------------------------------------------------------

// countNotesContaining returns how many Notes carry substr anywhere in Msg.
func countNotesContaining(notes []diag.Label, substr string) int {
	n := 0
	for _, lbl := range notes {
		if strings.Contains(lbl.Msg, substr) {
			n++
		}
	}
	return n
}

// Test 1 — literal regex constraint.
//
// `command: =~"^rm "` vs `"ls"`. f.Value is an *ast.UnaryExpr — cheap gate
// does not fire. ruleNext.Eval().Syntax(Raw) and f.Value format identically
// — divergence gate does not fire. Therefore no `want:` note is emitted;
// Primary.Msg is empty (Title carries "leaf constraint failed").
func TestNoRestate_LiteralRegex_NoWantAndNoConstraintMsg(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "lit_regex.cue", `{
	when: {command: =~"^rm "}
	then: deny: {rule_id: "r", reason: "nope"}
}`)
	rule := loadOne(t, dir)

	input := compileVal(t, `{command: "ls"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	if strings.Contains(d.Primary.Msg, "constraint not satisfied") {
		t.Errorf("Primary.Msg = %q must not restate the Title", d.Primary.Msg)
	}
	if d.Primary.Msg != "" {
		t.Errorf("Primary.Msg = %q, want \"\" (Reasons carry the payload; no legacy Msg)",
			d.Primary.Msg)
	}
	if n := countNotesContaining(d.Notes, "want:"); n != 0 {
		t.Errorf("literal regex must NOT emit `want:` Note; got %d such Notes in %+v",
			n, d.Notes)
	}
	if n := countNotesContaining(d.Notes, "got:"); n != 0 {
		t.Errorf("legacy `got:` Note must be dropped; got %d such Notes in %+v",
			n, d.Notes)
	}
}

// Test 2 — literal bound constraint (single-conjunct).
//
// `count: >=5` vs `3`. The bound `>=5` is a UnaryExpr, not Ident/Selector.
// Formatted forms match. No `want:`, no Primary.Msg restating the title,
// no legacy `got:`.
func TestNoRestate_LiteralBound_NoWantNoGot(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "lit_bound.cue", `{
	when: {count: >=5}
	then: deny: {rule_id: "r", reason: "nope"}
}`)
	rule := loadOne(t, dir)

	input := compileVal(t, `{count: 3}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	if d.Primary.Msg != "" {
		t.Errorf("Primary.Msg = %q, want empty", d.Primary.Msg)
	}
	if n := countNotesContaining(d.Notes, "want:"); n != 0 {
		t.Errorf("literal bound must NOT emit `want:`; got %d in %+v", n, d.Notes)
	}
	if n := countNotesContaining(d.Notes, "got:"); n != 0 {
		t.Errorf("legacy `got:` must be dropped; got %d in %+v", n, d.Notes)
	}
}

// Test 3 — reference constraint (cheap AST gate fires).
//
// Define a named regex alias `#DangerousCmds: =~"^rm "` as a definition in the
// same rule file, then constrain `command` by the selector `rule.#DangerousCmds`.
// f.Value is *ast.SelectorExpr → cheap gate fires → `want:` IS emitted, and
// the expanded form carries the regex body.
func TestNoRestate_ReferenceConstraint_WantEmittedWithExpandedForm(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	// #DangerousCmds is a hidden definition at the file top level; the rule
	// references it as a selector so f.Value is *ast.SelectorExpr.
	body := `package rules

#DangerousCmds: =~"^rm "

rule: {
	when: {command: #DangerousCmds}
	then: deny: {rule_id: "r", reason: "nope"}
}
`
	if err := os.WriteFile(filepath.Join(dir, "ref.cue"), []byte(body), 0o600); err != nil {
		t.Fatalf("write rule: %v", err)
	}
	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(rules))
	}
	rule := rules[0]

	input := compileValKind(t, `{command: "ls"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	if n := countNotesContaining(d.Notes, "want:"); n == 0 {
		t.Errorf("reference constraint must emit `want:` (cheap AST gate); got 0 such Notes in %+v",
			d.Notes)
	}
	// The expanded form should mention the regex body, not just the selector.
	foundExpanded := false
	for _, lbl := range d.Notes {
		if strings.Contains(lbl.Msg, "want:") && strings.Contains(lbl.Msg, "^rm ") {
			foundExpanded = true
			break
		}
	}
	if !foundExpanded {
		t.Errorf("want: Note must carry the expanded form (regex body `^rm `); notes=%+v",
			d.Notes)
	}
}

// Test 4 — format-divergence gate.
//
// f.Value is `_int` (an alias declared in the same file). ruleNext is narrowed
// via unification with a stdlib-style definition `#Positive: _int & >=0` to
// `int & >=0`. Formatted forms differ → format-divergence gate fires →
// `want:` IS emitted with the expanded form `int & >=0`.
//
// We construct this by writing the stdlib alias alongside the rule in the
// same package; the when constraint uses `_int & #Positive`. Here f.Value
// is a BinaryExpr (`_int & #Positive`), so the cheap gate does not apply.
// The format-divergence gate must catch the fact that ruleNext.Expr()
// formats to a form that differs from the source text.
func TestNoRestate_FormatDivergenceGate_WantEmitted(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	body := `package rules

_int: int

#Positive: _int & >=0

rule: {
	when: {count: _int & #Positive}
	then: deny: {rule_id: "r", reason: "nope"}
}
`
	if err := os.WriteFile(filepath.Join(dir, "narrow.cue"), []byte(body), 0o600); err != nil {
		t.Fatalf("write rule: %v", err)
	}
	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(rules))
	}
	rule := rules[0]

	input := compileValKind(t, `{count: -1}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	// Require the want: note. The precise formatted form is CUE's choice, so
	// assert only that a want: note is present AND that it refers to the
	// narrowing bound `>=0` (the key information the source text omits).
	var wantMsg string
	for _, lbl := range d.Notes {
		if strings.Contains(lbl.Msg, "want:") {
			wantMsg = lbl.Msg
			break
		}
	}
	if wantMsg == "" {
		t.Fatalf("format-divergence case must emit `want:`; notes=%+v", d.Notes)
	}
	if !strings.Contains(wantMsg, ">=0") {
		t.Errorf("want: Note = %q, must carry the narrowing bound `>=0`", wantMsg)
	}
}

// Test 5 — Title never appears twice in rendered output.
//
// Run the renderer on the literal-regex diagnostic (Test 1 fixture) and
// assert the title string "leaf constraint failed" appears exactly once.
// Guards against a regression where Primary.Msg carries a "constraint not
// satisfied" style restatement that the renderer would also surface.
func TestNoRestate_RenderedOutput_TitleAppearsOnce(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "render.cue", `{
	when: {command: =~"^rm "}
	then: deny: {rule_id: "r", reason: "nope"}
}`)
	rule := loadOne(t, dir)

	input := compileVal(t, `{command: "ls"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	src := diag.NewFileCache()
	rendered := diag.Render(d, src)

	const title = "leaf constraint failed"
	count := strings.Count(rendered, title)
	if count != 1 {
		t.Errorf("title %q must appear exactly once in rendered output; got %d occurrences.\n--- rendered ---\n%s",
			title, count, rendered)
	}
	// Also guard against "constraint not satisfied" leaking into the render.
	if strings.Contains(rendered, "constraint not satisfied") {
		t.Errorf("rendered output must not contain the legacy `constraint not satisfied` restatement.\n--- rendered ---\n%s",
			rendered)
	}
}
