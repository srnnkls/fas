package evaluator_test

import (
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
	"github.com/srnnkls/fas/internal/evaluator"
)

// -----------------------------------------------------------------------------
// T4 — Conjunct decomposition via cue.Value.Expr()
//
// These tests exercise the new behavior: when Subsume fails AND kinds are not
// disjoint (T3 short-circuit doesn't fire), localize walks `ruleNext.Expr()`
// to enumerate conjuncts and checks each against the input. Each FAILING
// conjunct contributes one ConjunctFailed{Expr, Span, Sub: nil} entry to the
// Primary Label's Reasons slice (T5/T6/T7 will populate Sub later).
//
// Fixtures reuse the kindAliasesPreamble workaround from localize_kind_test.go
// to bypass the bare-kind lint. Helpers (writeKindFile, loadOneRule,
// compileValKind, findDiag, countDiagsWithCode) live in localize_kind_test.go.
// -----------------------------------------------------------------------------

// conjunctReasons returns the slice of ConjunctFailed entries inside
// d.Primary.Reasons, preserving source order. Non-ConjunctFailed entries are
// skipped — the caller asserts length separately.
func conjunctReasons(reasons []diag.Reason) []diag.ConjunctFailed {
	out := make([]diag.ConjunctFailed, 0, len(reasons))
	for _, r := range reasons {
		if cf, ok := r.(diag.ConjunctFailed); ok {
			out = append(out, cf)
		}
	}
	return out
}

// Single conjunct failure: `count: int & >=5 & <=10` vs `count: 3` must produce
// exactly one ConjunctFailed entry whose Expr is the failing bound `>=5`, and
// whose Span underlines just that bound — not the whole `int & >=5 & <=10`.
func TestLocalize_Conjunct_SingleFailure_E0301WithOneReason(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "single_fail.cue", kindAliasesPreamble+`rule: {
	when: {count: _int & >=5 & <=10}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: 3}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	cfs := conjunctReasons(d.Primary.Reasons)
	if len(cfs) != 1 {
		t.Fatalf("Primary.Reasons (ConjunctFailed entries) length = %d, want 1; reasons=%+v",
			len(cfs), d.Primary.Reasons)
	}
	if len(d.Primary.Reasons) != 1 {
		t.Fatalf("Primary.Reasons total length = %d, want 1 (no non-ConjunctFailed entries); reasons=%+v",
			len(d.Primary.Reasons), d.Primary.Reasons)
	}
	// Expr source substring — accept `>=5` or `>= 5` (format.Node may normalize).
	got := cfs[0].Expr
	norm := strings.ReplaceAll(got, " ", "")
	if norm != ">=5" {
		t.Errorf("ConjunctFailed.Expr = %q (normalized %q), want substring %q", got, norm, ">=5")
	}
	// Span must NOT cover the whole `int & >=5 & <=10` — underline just the failing conjunct.
	// len("int & >=5 & <=10") == 16; len(">=5") == 3. Span.Length should be <= 4 or so (`>=5` or `>= 5`).
	if cfs[0].Span.Length <= 0 {
		t.Errorf("ConjunctFailed.Span.Length = %d, want >0", cfs[0].Span.Length)
	}
	if cfs[0].Span.Length >= len("int & >=5 & <=10") {
		t.Errorf("ConjunctFailed.Span.Length = %d, want < %d (must underline just the failing conjunct, not the whole chain)",
			cfs[0].Span.Length, len("int & >=5 & <=10"))
	}
	// T5: bound conjunct now carries a BoundViolation Sub. Detailed
	// BoundViolation field assertions live in bound_test.go; here we just
	// check the Sub slot is populated so T4's pairing invariant holds.
	if cfs[0].Sub == nil {
		t.Errorf("ConjunctFailed.Sub = nil, want a BoundViolation for `>=5` (T5)")
	}
}

// Multiple conjuncts failing at the same leaf stack as multiple singular
// ConjunctFailed entries on the Label, preserving source order (I3a).
// Uses two regex conjuncts so no stdlib import is required.
func TestLocalize_Conjunct_MultipleFailures_SourceOrderPreserved(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "multi_fail.cue", kindAliasesPreamble+`rule: {
	when: {x: _string & =~"^[a-z]+$" & =~"Z"}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	// `"AB"` is uppercase (fails `^[a-z]+$`) AND has no "Z" (fails `=~"Z"`).
	input := compileValKind(t, `{x: "AB"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	cfs := conjunctReasons(d.Primary.Reasons)
	if len(cfs) != 2 {
		t.Fatalf("Primary.Reasons (ConjunctFailed entries) length = %d, want 2; reasons=%+v",
			len(cfs), d.Primary.Reasons)
	}

	// Source order: `^[a-z]+$` first, `Z` second.
	first, second := cfs[0].Expr, cfs[1].Expr
	if !strings.Contains(first, "[a-z]") {
		t.Errorf("Reasons[0].Expr = %q, want substring %q (first regex by source order)", first, "[a-z]")
	}
	if !strings.Contains(second, "Z") || strings.Contains(second, "[a-z]") {
		t.Errorf("Reasons[1].Expr = %q, want the second regex (substring %q, not the first)", second, "Z")
	}
}

// All conjuncts passing produces no diagnostic for the leaf — sanity baseline
// that proves the conjunct walker isn't spuriously emitting ConjunctFailed.
func TestLocalize_Conjunct_AllPass_NoDiagnostic(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "all_pass.cue", kindAliasesPreamble+`rule: {
	when: {count: _int & >=0 & <=10}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: 5}`)

	matches, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(matches) != 1 {
		t.Errorf("all conjuncts pass: rule must match; got %d matches", len(matches))
	}
	for _, d := range diags {
		if cfs := conjunctReasons(d.Primary.Reasons); len(cfs) > 0 {
			t.Errorf("all-pass case produced ConjunctFailed reasons: %+v", d)
		}
	}
}

// Negation bound `!=7` must surface as a ConjunctFailed when violated.
func TestLocalize_Conjunct_NegationBound_OneFailure(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "neg_bound.cue", kindAliasesPreamble+`rule: {
	when: {count: _int & !=7}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: 7}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	cfs := conjunctReasons(d.Primary.Reasons)
	if len(cfs) != 1 {
		t.Fatalf("Primary.Reasons length = %d, want 1; reasons=%+v", len(cfs), d.Primary.Reasons)
	}
	norm := strings.ReplaceAll(cfs[0].Expr, " ", "")
	if norm != "!=7" {
		t.Errorf("ConjunctFailed.Expr = %q (normalized %q), want %q", cfs[0].Expr, norm, "!=7")
	}
}

// Nested `(A & B) & C` flattens into a single-level conjunct list before the
// multiplicity check — no wrapper-shaped ConjunctFailed on the outer node.
func TestLocalize_Conjunct_NestedAndFlattens(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "nested_and.cue", kindAliasesPreamble+`rule: {
	when: {count: (_int & >=5) & <=10}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: 3}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	cfs := conjunctReasons(d.Primary.Reasons)
	if len(cfs) != 1 {
		t.Fatalf("nested (A & B) & C must flatten; Primary.Reasons length = %d, want 1; reasons=%+v",
			len(cfs), d.Primary.Reasons)
	}
	norm := strings.ReplaceAll(cfs[0].Expr, " ", "")
	if norm != ">=5" {
		t.Errorf("ConjunctFailed.Expr = %q (normalized %q), want %q — nested wrapper must NOT appear",
			cfs[0].Expr, norm, ">=5")
	}
	// Defensive: the Expr must not be a parenthesised wrapper like `(int & >=5)`.
	if strings.Contains(cfs[0].Expr, "&") {
		t.Errorf("ConjunctFailed.Expr = %q contains `&` — nested conjunction leaked through (should have been flattened)",
			cfs[0].Expr)
	}
}

// Each ConjunctFailed.Span must carry a valid File + non-zero Line/Col + a
// Length matching the conjunct's source-expression length (not the whole
// constraint chain). Don't pin specific line numbers — pin structural
// properties so fixture layout tweaks don't break the test.
func TestLocalize_Conjunct_SpanShape(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "span_shape.cue", kindAliasesPreamble+`rule: {
	when: {count: _int & >=5 & <=10}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: 3}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	cfs := conjunctReasons(d.Primary.Reasons)
	if len(cfs) != 1 {
		t.Fatalf("Primary.Reasons length = %d, want 1; reasons=%+v", len(cfs), d.Primary.Reasons)
	}
	sp := cfs[0].Span
	if sp.File == "" {
		t.Errorf("Span.File is empty; want a resolved filename")
	}
	if sp.Line <= 0 {
		t.Errorf("Span.Line = %d, want >0", sp.Line)
	}
	if sp.Col <= 0 {
		t.Errorf("Span.Col = %d, want >0", sp.Col)
	}
	// Span.Length must equal the rune-count of the rendered Expr: the span
	// over the source should cover exactly the characters of the conjunct as
	// rendered (format.Node). ASCII-only fixture, so len() == rune-count.
	if sp.Length != len(cfs[0].Expr) {
		t.Errorf("Span.Length %d should equal len(Expr=%q)=%d",
			sp.Length, cfs[0].Expr, len(cfs[0].Expr))
	}
}

// A single-literal constraint (no `&` chain — `name: "Bash"`) still produces
// an E0301 on failure. T4 accepts either:
//   - the conjunct walker synthesises one ConjunctFailed{Expr: `"Bash"`, ...}
//     (Reasons length 1), OR
//   - the legacy Msg fallthrough path (NF5) — Reasons length 0, diagnostic
//     still emits with Code E0301 and the message carries the expectation.
//
// T5/T6 will tighten this once `Sub` populates concrete failure types (e.g.,
// a dedicated EnumMismatch for singular literal mismatches).
func TestLocalize_Conjunct_SingleLiteralConstraint(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "literal.cue", kindAliasesPreamble+`rule: {
	when: {name: "Bash"}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{name: "Read"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")

	cfs := conjunctReasons(d.Primary.Reasons)
	switch {
	case len(cfs) == 1 && len(d.Primary.Reasons) == 1:
		// Conjunct-walker path: ConjunctFailed carries the literal source.
		t.Logf("literal constraint: ConjunctFailed path (Expr=%q)", cfs[0].Expr)
		if !strings.Contains(cfs[0].Expr, `"Bash"`) {
			t.Errorf("ConjunctFailed.Expr = %q, want to contain %q", cfs[0].Expr, `"Bash"`)
		}
	case len(d.Primary.Reasons) == 0:
		// Legacy Msg fallthrough (NF5): diagnostic emits without a Reason.
		// Acceptable for T4; T5/T6 will synthesise a concrete failure type.
		t.Logf("literal constraint: legacy Msg fallthrough path (no Reasons)")
	default:
		t.Fatalf("literal constraint: unexpected Reasons shape (ConjunctFailed=%d, total=%d); want 1 ConjunctFailed or 0 total; reasons=%+v",
			len(cfs), len(d.Primary.Reasons), d.Primary.Reasons)
	}
}
