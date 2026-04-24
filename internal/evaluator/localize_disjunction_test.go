package evaluator_test

import (
	"strings"
	"testing"

	"github.com/srnnkls/quae/internal/config"
	"github.com/srnnkls/quae/internal/diag"
	"github.com/srnnkls/quae/internal/evaluator"
)

// -----------------------------------------------------------------------------
// T7 — Localize: disjunction arm ranking (integration).
//
// These black-box tests pin the wiring between walkStruct and rankArms:
// when a leaf's f.Value is a `A | B | C` BinaryExpr that fails to subsume
// the input, the diagnostic must be E0401 with Title "no disjunction arm
// matched" and Primary.Reasons[0] must be a DisjunctionFailed carrying a
// ranked []ArmResult (sorted by Score desc, ties by source order).
//
// Renderer behavior (caret frames, flat summary, colors) is OUT OF SCOPE
// for T7 — that lands in T12. These tests assert only the data-layer
// shape: Reason type, Score ordering, Span presence.
//
// Fixtures reuse writeKindFile / loadOneRule / compileValKind / findDiag
// from localize_kind_test.go.
// -----------------------------------------------------------------------------

// disjunctionReason finds the first DisjunctionFailed on d.Primary.Reasons or
// fails the test.
func disjunctionReason(t *testing.T, reasons []diag.Reason) diag.DisjunctionFailed {
	t.Helper()
	for _, r := range reasons {
		if df, ok := r.(diag.DisjunctionFailed); ok {
			return df
		}
	}
	t.Fatalf("no DisjunctionFailed reason on Primary.Reasons; got=%+v", reasons)
	return diag.DisjunctionFailed{}
}

// runDisjunctionTest loads a rule with a `when` body containing a leaf-level
// disjunction and returns the E0401 diagnostic produced against `input`.
func runDisjunctionTest(t *testing.T, when, input string) diag.Diagnostic {
	t.Helper()
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	body := `package rules

rule: {
	when: ` + when + `
	then: deny: {rule_id: "r", reason: "nope"}
}
`
	writeKindFile(t, dir, "disj.cue", body)
	rule := loadOneRule(t, dir)

	val := compileValKind(t, input)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, val)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return findDiag(t, diags, "E0401")
}

// -----------------------------------------------------------------------------
// 1. String-arm closeness: `"Bash" | "Read" | "Write"` vs `"Rd"` → Arms[0]=Read.
// -----------------------------------------------------------------------------

func TestLocalize_Disjunction_StringClosestArmWins(t *testing.T) {
	d := runDisjunctionTest(t,
		`{tool_name: "Bash" | "Read" | "Write"}`,
		`{tool_name: "Rd"}`)

	if d.Code != "E0401" {
		t.Errorf("Code = %q, want %q", d.Code, "E0401")
	}
	if d.Title != "no disjunction arm matched" {
		t.Errorf("Title = %q, want %q", d.Title, "no disjunction arm matched")
	}

	df := disjunctionReason(t, d.Primary.Reasons)
	if len(df.Arms) != 3 {
		t.Fatalf("DisjunctionFailed.Arms length = %d, want 3; arms=%+v", len(df.Arms), df.Arms)
	}

	// Top arm must be "Read" — closest by edit distance to "Rd".
	if !strings.Contains(df.Arms[0].Arm, `"Read"`) {
		t.Errorf(`Arms[0].Arm = %q, want to contain "\"Read\""`, df.Arms[0].Arm)
	}
	// Scores must be non-increasing.
	for i := 1; i < len(df.Arms); i++ {
		if df.Arms[i-1].Score < df.Arms[i].Score {
			t.Errorf("Arms sorted ascending at i=%d: Score[%d]=%d < Score[%d]=%d",
				i, i-1, df.Arms[i-1].Score, i, df.Arms[i].Score)
		}
	}
}

// -----------------------------------------------------------------------------
// 2. No-close-arm case: `"Bash" | "Read" | "Write"` vs `true` (bool) → every
// arm has incompatible kind. Top arm's Score must be strictly below
// ScoreKindMatch (Arms slice still fully populated — suppression is a
// renderer decision, not a data-layer one; data layer always carries arms).
// -----------------------------------------------------------------------------

func TestLocalize_Disjunction_NoCloseArm_ScoreBelowKindMatch(t *testing.T) {
	d := runDisjunctionTest(t,
		`{tool_name: "Bash" | "Read" | "Write"}`,
		`{tool_name: true}`)

	df := disjunctionReason(t, d.Primary.Reasons)
	if len(df.Arms) != 3 {
		t.Fatalf("DisjunctionFailed.Arms length = %d, want 3 (arms always populated at data layer); arms=%+v",
			len(df.Arms), df.Arms)
	}
	// All three string arms have kind string; input is bool. None share kind.
	// Pin: top arm's Score is below ScoreKindMatch. Exact value implementer-
	// chosen (0 is typical) but MUST satisfy the threshold contract.
	if df.Arms[0].Score >= evaluator.ScoreKindMatch {
		t.Errorf("Arms[0].Score = %d, want < ScoreKindMatch %d (no arm shares kind with bool input)",
			df.Arms[0].Score, evaluator.ScoreKindMatch)
	}
	// Source order preserved on an all-below-threshold tie: Bash, Read, Write.
	wantOrder := []string{`"Bash"`, `"Read"`, `"Write"`}
	for i, w := range wantOrder {
		if !strings.Contains(df.Arms[i].Arm, w) {
			t.Errorf("Arms[%d].Arm = %q, want to contain %q (source order on no-close-arm tie)",
				i, df.Arms[i].Arm, w)
		}
	}
}

// -----------------------------------------------------------------------------
// 3. Source order preserved on ties: two arms of equal score — the first in
// source wins position 0. Using `"Bash" | "Read"` vs `"ZZZZ"` (equidistant in
// edit distance: Bash=4, Read=4) guarantees a tie.
// -----------------------------------------------------------------------------

func TestLocalize_Disjunction_SourceOrderOnEqualScore(t *testing.T) {
	d := runDisjunctionTest(t,
		`{tool_name: "Bash" | "Read"}`,
		`{tool_name: "ZZZZ"}`)

	df := disjunctionReason(t, d.Primary.Reasons)
	if len(df.Arms) != 2 {
		t.Fatalf("DisjunctionFailed.Arms length = %d, want 2; arms=%+v", len(df.Arms), df.Arms)
	}
	// On equal score, source order: Bash first, Read second.
	if df.Arms[0].Score == df.Arms[1].Score {
		if !strings.Contains(df.Arms[0].Arm, `"Bash"`) {
			t.Errorf("tie at score %d: Arms[0].Arm = %q, want \"Bash\" (first in source)",
				df.Arms[0].Score, df.Arms[0].Arm)
		}
		if !strings.Contains(df.Arms[1].Arm, `"Read"`) {
			t.Errorf("tie at score %d: Arms[1].Arm = %q, want \"Read\" (second in source)",
				df.Arms[1].Score, df.Arms[1].Arm)
		}
	} else {
		t.Logf("scores diverged (Bash=%d, Read=%d) — tie-breaking path not exercised",
			df.Arms[0].Score, df.Arms[1].Score)
	}
}

// -----------------------------------------------------------------------------
// 4. Each ArmResult.Span has valid file/line/col + non-zero Length.
// -----------------------------------------------------------------------------

func TestLocalize_Disjunction_ArmSpansPopulated(t *testing.T) {
	d := runDisjunctionTest(t,
		`{tool_name: "Bash" | "Read" | "Write"}`,
		`{tool_name: "Rd"}`)

	df := disjunctionReason(t, d.Primary.Reasons)

	for i, a := range df.Arms {
		if a.Span.File == "" {
			t.Errorf("Arms[%d].Span.File is empty; arm=%q", i, a.Arm)
		}
		if a.Span.Line <= 0 {
			t.Errorf("Arms[%d].Span.Line = %d, want >0", i, a.Span.Line)
		}
		if a.Span.Col <= 0 {
			t.Errorf("Arms[%d].Span.Col = %d, want >0", i, a.Span.Col)
		}
		if a.Span.Length <= 0 {
			t.Errorf("Arms[%d].Span.Length = %d, want >0 (covers arm source)", i, a.Span.Length)
		}
	}
}
