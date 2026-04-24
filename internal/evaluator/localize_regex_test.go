package evaluator_test

import (
	"strings"
	"testing"

	"github.com/srnnkls/quae/internal/config"
	"github.com/srnnkls/quae/internal/diag"
	"github.com/srnnkls/quae/internal/evaluator"
)

// -----------------------------------------------------------------------------
// T6 — Localize: regex divergence Sub population
//
// These black-box integration tests pin the wiring between localize and the
// regex_diverge helper: when a failing conjunct of a `&` chain is a CUE regex
// constraint (UnaryExpr with op `=~` wrapping a BasicLit), the corresponding
// ConjunctFailed.Sub must be a `RegexMismatch{Pattern, Input, DivergeAt}`.
//
// Unit-level contract for the helper itself lives in regex_diverge_test.go.
// Here we only assert that the Sub slot is populated for regex conjuncts and
// that T5's BoundViolation path is not stomped when both conjunct kinds
// appear in the same rule.
//
// Fixtures reuse the kindAliasesPreamble + writeKindFile / loadOneRule /
// compileValKind / findDiag helpers from localize_kind_test.go.
// -----------------------------------------------------------------------------

// regexMismatchSub walks d.Primary.Reasons, finds the ConjunctFailed whose
// Expr (after whitespace strip) matches wantExpr, and returns its Sub as
// RegexMismatch. Fails the test when no such entry exists or when Sub is
// not a RegexMismatch.
func regexMismatchSub(t *testing.T, reasons []diag.Reason, wantExpr string) diag.RegexMismatch {
	t.Helper()
	norm := func(s string) string { return strings.ReplaceAll(s, " ", "") }
	for _, r := range reasons {
		cf, ok := r.(diag.ConjunctFailed)
		if !ok {
			continue
		}
		if norm(cf.Expr) != wantExpr {
			continue
		}
		if cf.Sub == nil {
			t.Fatalf("ConjunctFailed(%q).Sub is nil, want RegexMismatch; reasons=%+v",
				cf.Expr, reasons)
		}
		rm, ok := cf.Sub.(diag.RegexMismatch)
		if !ok {
			t.Fatalf("ConjunctFailed(%q).Sub type = %T, want diag.RegexMismatch",
				cf.Expr, cf.Sub)
		}
		return rm
	}
	t.Fatalf("no ConjunctFailed with Expr matching %q; reasons=%+v", wantExpr, reasons)
	return diag.RegexMismatch{}
}

// runLeafRegexTest loads a rule with the given `when` constraint body, runs
// Evaluate against `input`, and returns the E0301 diagnostic.
func runLeafRegexTest(t *testing.T, when, input string) diag.Diagnostic {
	t.Helper()
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	body := kindAliasesPreamble + `rule: {
	when: ` + when + `
	then: deny: {rule_id: "r", reason: "nope"}
}
`
	writeKindFile(t, dir, "regex.cue", body)
	rule := loadOneRule(t, dir)

	val := compileValKind(t, input)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, val)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return findDiag(t, diags, "E0301")
}

// Rule `command: =~"^rm "`, input `command: "ls"` → primary Reasons[0] is a
// ConjunctFailed whose Sub is RegexMismatch with Pattern, Input, DivergeAt=0.
func TestLocalize_Regex_PrefixFails_DivergeAtZero(t *testing.T) {
	// Multi-conjunct chain so the walker fires (T4 requires len(conjuncts) >= 2
	// before emitting ConjunctFailed reasons). `_string` always passes; only
	// the regex fails.
	d := runLeafRegexTest(t, `{command: _string & =~"^rm "}`, `{command: "ls"}`)

	rm := regexMismatchSub(t, d.Primary.Reasons, `=~"^rm"`)
	if rm.Pattern != "^rm " {
		t.Errorf("RegexMismatch.Pattern = %q, want %q", rm.Pattern, "^rm ")
	}
	if rm.Input != "ls" {
		t.Errorf("RegexMismatch.Input = %q, want %q", rm.Input, "ls")
	}
	if rm.DivergeAt != 0 {
		t.Errorf("RegexMismatch.DivergeAt = %d, want 0", rm.DivergeAt)
	}
}

// Rule `command: =~"^rm "`, input `command: "rm-rf /"` → matches `rm` (2
// bytes), diverges on `-` where ` ` was expected. DivergeAt=2.
func TestLocalize_Regex_PartialMatch_DivergeAtTwo(t *testing.T) {
	d := runLeafRegexTest(t, `{command: _string & =~"^rm "}`, `{command: "rm-rf /"}`)

	rm := regexMismatchSub(t, d.Primary.Reasons, `=~"^rm"`)
	if rm.Pattern != "^rm " {
		t.Errorf("RegexMismatch.Pattern = %q, want %q", rm.Pattern, "^rm ")
	}
	if rm.Input != "rm-rf /" {
		t.Errorf("RegexMismatch.Input = %q, want %q", rm.Input, "rm-rf /")
	}
	if rm.DivergeAt != 2 {
		t.Errorf("RegexMismatch.DivergeAt = %d, want 2 (matched 'rm', ' ' vs '-')",
			rm.DivergeAt)
	}
}

// Mixed chain: a bound conjunct and a regex conjunct in the same leaf must
// carry kind-appropriate Subs — BoundViolation for the bound, RegexMismatch
// for the regex. Neither stomps the other.
//
// Setup: two sibling leaves in the same rule — `port: int & >=80 & <=443`
// and `command: string & =~"^rm "`. Input violates both: port=79 (below >=80)
// and command="ls" (regex fails). This yields two separate diagnostics (one
// per leaf per F13 policy), each with its own Sub type.
func TestLocalize_Regex_MixedWithBound_DoesNotStompBoundSub(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	body := kindAliasesPreamble + `rule: {
	when: {
		port:    _int & >=80 & <=443
		command: _string & =~"^rm "
	}
	then: deny: {rule_id: "r", reason: "nope"}
}
`
	writeKindFile(t, dir, "mixed.cue", body)
	rule := loadOneRule(t, dir)

	val := compileValKind(t, `{port: 79, command: "ls"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, val)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	// Locate each diagnostic by inspecting Reasons: the one with a
	// BoundViolation Sub is the port leaf; the one with a RegexMismatch
	// Sub is the command leaf.
	var haveBound, haveRegex bool
	for _, d := range diags {
		if d.Code != "E0301" {
			continue
		}
		for _, r := range d.Primary.Reasons {
			cf, ok := r.(diag.ConjunctFailed)
			if !ok {
				continue
			}
			switch cf.Sub.(type) {
			case diag.BoundViolation:
				haveBound = true
			case diag.RegexMismatch:
				haveRegex = true
			}
		}
	}
	if !haveBound {
		t.Errorf("no BoundViolation Sub found across diagnostics; regex path stomped bound Sub; diags=%+v",
			diags)
	}
	if !haveRegex {
		t.Errorf("no RegexMismatch Sub found across diagnostics; bound path stomped regex Sub; diags=%+v",
			diags)
	}
}

// Complex pattern with top-level alternation: `=~"^foo|bar$"` — the helper
// bails gracefully. Sub is still a RegexMismatch (so renderer dispatch works
// uniformly), but DivergeAt = -1 signals "no useful divergence; fall back to
// 'regex did not match'".
func TestLocalize_Regex_ComplexPattern_DivergeAtMinusOne(t *testing.T) {
	d := runLeafRegexTest(t, `{command: _string & =~"^foo|bar$"}`, `{command: "baz"}`)

	rm := regexMismatchSub(t, d.Primary.Reasons, `=~"^foo|bar$"`)
	if rm.Pattern != "^foo|bar$" {
		t.Errorf("RegexMismatch.Pattern = %q, want %q", rm.Pattern, "^foo|bar$")
	}
	if rm.Input != "baz" {
		t.Errorf("RegexMismatch.Input = %q, want %q", rm.Input, "baz")
	}
	if rm.DivergeAt != -1 {
		t.Errorf("RegexMismatch.DivergeAt = %d, want -1 (top-level alternation bails per spec)",
			rm.DivergeAt)
	}
}
