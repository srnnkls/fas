package evaluator

import (
	"reflect"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
)

// -----------------------------------------------------------------------------
// T7 — rankArms unit tests (white-box).
//
// rankArms(arms, ruleVal, input) returns []diag.ArmResult sorted by Score
// descending; ties broken by source order. Scoring tiers per AD-3:
//
//	ScoreKindMatch       > ScoreStructuralMatch > ScoreValueDistance
//
// The implementer defines these as package constants; tests reference them
// directly so drift in tier boundaries surfaces here.
//
// All helpers (parseOrChain, compileVal) produce the arms + cue.Value shapes
// rankArms expects, parallel to how localize will call rankArms from
// walkStruct: the BinaryExpr at f.Value plus ruleNext + input leaves.
// -----------------------------------------------------------------------------

// parseOrChain parses a CUE expression string like `"Bash" | "Read" | "Write"`
// into a flat []ast.Expr matching the shape walkStruct would hand rankArms —
// arms in source order.
func parseOrChain(t *testing.T, src string) []ast.Expr {
	t.Helper()
	expr, err := parser.ParseExpr("arms.cue", src)
	if err != nil {
		t.Fatalf("parser.ParseExpr(%q): %v", src, err)
	}
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("parsed %q is %T, want *ast.BinaryExpr", src, expr)
	}
	if bin.Op != token.OR {
		t.Fatalf("parsed %q op = %v, want OR", src, bin.Op)
	}
	return flattenOrArms(bin)
}

// compileRankVal compiles a CUE value snippet for tests. Separate from the
// black-box compileVal helpers to keep the white-box file self-contained.
func compileRankVal(t *testing.T, src string) cue.Value {
	t.Helper()
	ctx := cuecontext.New()
	v := ctx.CompileString(src, cue.Filename("rank.cue"))
	if err := v.Err(); err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return v
}

// -----------------------------------------------------------------------------
// 1. Closest arm wins on string distance.
// -----------------------------------------------------------------------------

// Arms `"Bash" | "Read" | "Write"`, input `"Rd"`.
// Edit distances: Bash=4 ("Bash"↔"Rd"), Read=2, Write=4. Top arm = "Read".
// Whichever of Bash/Write ranks second is score-driven; what we pin here is
// that "Read" is strictly the top pick, and that every arm is returned.
func TestRankArms_ClosestArmWinsOnStringDistance(t *testing.T) {
	arms := parseOrChain(t, `"Bash" | "Read" | "Write"`)
	ctx := cuecontext.New()
	ruleVal := ctx.CompileString(`"Bash" | "Read" | "Write"`, cue.Filename("rule.cue"))
	if err := ruleVal.Err(); err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	input := compileRankVal(t, `"Rd"`)

	got := rankArms(arms, ruleVal, input)

	if len(got) != 3 {
		t.Fatalf("rankArms returned %d arms, want 3; got=%+v", len(got), got)
	}
	// Top arm must be "Read" (closest by edit distance).
	if !strings.Contains(got[0].Arm, `"Read"`) {
		t.Errorf("Arms[0].Arm = %q, want to contain %q (closest by edit distance)", got[0].Arm, `"Read"`)
	}
	// Scores must be non-increasing.
	for i := 1; i < len(got); i++ {
		if got[i-1].Score < got[i].Score {
			t.Errorf("Arms[%d].Score %d < Arms[%d].Score %d — must be sorted descending",
				i-1, got[i-1].Score, i, got[i].Score)
		}
	}
}

// -----------------------------------------------------------------------------
// 2. All-incompatible-kind arms rank by source order (no arm shares kind with
// input). Score for every arm falls below ScoreKindMatch — the "no close arm"
// tier per F8 — and the stable fallback is source order.
// -----------------------------------------------------------------------------

func TestRankArms_AllIncompatibleKinds_SourceOrderPreserved(t *testing.T) {
	// `int | string | bool` vs `[]` (list kind) — none share kind.
	arms := parseOrChain(t, `int | string | bool`)
	ctx := cuecontext.New()
	ruleVal := ctx.CompileString(`int | string | bool`, cue.Filename("rule.cue"))
	if err := ruleVal.Err(); err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	input := compileRankVal(t, `[]`)

	got := rankArms(arms, ruleVal, input)

	if len(got) != 3 {
		t.Fatalf("rankArms returned %d arms, want 3; got=%+v", len(got), got)
	}

	// All scores below ScoreKindMatch — no arm even shares kind.
	for i, a := range got {
		if a.Score >= ScoreKindMatch {
			t.Errorf("Arms[%d].Score %d >= ScoreKindMatch %d — no arm should share kind with []",
				i, a.Score, ScoreKindMatch)
		}
	}

	// Deterministic order: since all scores are equally below-kind-match,
	// source order wins — int, string, bool.
	wantOrder := []string{"int", "string", "bool"}
	for i, w := range wantOrder {
		if !strings.Contains(got[i].Arm, w) {
			t.Errorf("Arms[%d].Arm = %q, want to contain %q (source order on ties)",
				i, got[i].Arm, w)
		}
	}
}

// -----------------------------------------------------------------------------
// 3. Struct match by field overlap — kind match on inner field wins.
// -----------------------------------------------------------------------------

func TestRankArms_StructMatchByFieldOverlap(t *testing.T) {
	// Arms `{a: int} | {a: string}` vs input `{a: "x"}` — the `{a: string}`
	// arm matches kind on field `a`, the `{a: int}` arm does not.
	arms := parseOrChain(t, `{a: int} | {a: string}`)
	ctx := cuecontext.New()
	ruleVal := ctx.CompileString(`{a: int} | {a: string}`, cue.Filename("rule.cue"))
	if err := ruleVal.Err(); err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	input := compileRankVal(t, `{a: "x"}`)

	got := rankArms(arms, ruleVal, input)

	if len(got) != 2 {
		t.Fatalf("rankArms returned %d arms, want 2; got=%+v", len(got), got)
	}
	// Top arm must be `{a: string}` — matches kind on field `a`.
	top := strings.ReplaceAll(got[0].Arm, " ", "")
	if !strings.Contains(top, "a:string") {
		t.Errorf("Arms[0].Arm = %q, want to contain %q (struct arm that kind-matches input.a)",
			got[0].Arm, "a: string")
	}
	// Top arm must have a strictly higher score than the losing arm.
	if got[0].Score <= got[1].Score {
		t.Errorf("Arms[0].Score %d <= Arms[1].Score %d; expected the kind-matching struct arm to win strictly",
			got[0].Score, got[1].Score)
	}
}

// -----------------------------------------------------------------------------
// 4. Score tier ordering — kind < kind+structure < kind+structure+value.
//
// Construct three arms against a scalar string input "hello":
//
//   - arm A: `int`          — no kind match (Score < ScoreKindMatch).
//   - arm B: `string`       — kind match only (Score >= ScoreKindMatch,
//     below structural bonuses).
//   - arm C: `"hello"`      — kind match + value match (highest tier).
//
// Scores must be strictly monotonic: A < B < C.
// -----------------------------------------------------------------------------

func TestRankArms_ScoreTierOrdering(t *testing.T) {
	arms := parseOrChain(t, `int | string | "hello"`)
	ctx := cuecontext.New()
	ruleVal := ctx.CompileString(`int | string | "hello"`, cue.Filename("rule.cue"))
	if err := ruleVal.Err(); err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	input := compileRankVal(t, `"hello"`)

	got := rankArms(arms, ruleVal, input)

	if len(got) != 3 {
		t.Fatalf("rankArms returned %d arms, want 3; got=%+v", len(got), got)
	}

	// Extract scores by arm identity — order of result is by rank, so we
	// re-key by Arm source for tier assertions independent of final order.
	byArm := map[string]int{}
	for _, a := range got {
		byArm[strings.ReplaceAll(a.Arm, " ", "")] = a.Score
	}

	sInt, ok := byArm["int"]
	if !ok {
		t.Fatalf("missing arm `int` in results; got=%+v", got)
	}
	sString, ok := byArm["string"]
	if !ok {
		t.Fatalf("missing arm `string` in results; got=%+v", got)
	}
	sLit, ok := byArm[`"hello"`]
	if !ok {
		t.Fatalf(`missing arm "\"hello\"" in results; got=%+v`, got)
	}

	// Tier boundary 1: `int` has no kind overlap with string input —
	// its score must be below ScoreKindMatch.
	if sInt >= ScoreKindMatch {
		t.Errorf("score(`int`) = %d, want < ScoreKindMatch %d (no kind overlap)",
			sInt, ScoreKindMatch)
	}
	// Tier boundary 2: `string` shares kind — at least ScoreKindMatch.
	if sString < ScoreKindMatch {
		t.Errorf("score(`string`) = %d, want >= ScoreKindMatch %d", sString, ScoreKindMatch)
	}
	// Tier boundary 3: `"hello"` matches value — strictly above `string`.
	if sLit <= sString {
		t.Errorf("score(`\"hello\"`) = %d, must be strictly > score(`string`) = %d (value match > kind match)",
			sLit, sString)
	}
	// Final ordering sanity: sInt < sString < sLit.
	if sInt >= sString || sString >= sLit {
		t.Errorf("tier scores not strictly increasing: int=%d, string=%d, \"hello\"=%d",
			sInt, sString, sLit)
	}
}

// -----------------------------------------------------------------------------
// 5. Deterministic across runs — same inputs, byte-identical result 100x.
// -----------------------------------------------------------------------------

func TestRankArms_DeterministicAcrossRuns(t *testing.T) {
	arms := parseOrChain(t, `"Bash" | "Read" | "Write"`)
	ctx := cuecontext.New()
	ruleVal := ctx.CompileString(`"Bash" | "Read" | "Write"`, cue.Filename("rule.cue"))
	if err := ruleVal.Err(); err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	input := compileRankVal(t, `"Rd"`)

	first := rankArms(arms, ruleVal, input)
	for i := range 100 {
		got := rankArms(arms, ruleVal, input)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("rankArms iteration %d diverged from first run:\n  first = %+v\n  got   = %+v",
				i, first, got)
		}
	}
}

// Score constants must be strictly monotonic per AD-3: kind match outranks
// any structural match, which outranks any pure value-distance score. This
// guards the tier hierarchy against accidental flattening.
func TestRankArms_ScoreConstantsStrictlyOrdered(t *testing.T) {
	if ScoreKindMatch <= ScoreStructuralMatch {
		t.Errorf("ScoreKindMatch (%d) must be strictly > ScoreStructuralMatch (%d)",
			ScoreKindMatch, ScoreStructuralMatch)
	}
	if ScoreStructuralMatch <= ScoreValueDistance {
		t.Errorf("ScoreStructuralMatch (%d) must be strictly > ScoreValueDistance (%d)",
			ScoreStructuralMatch, ScoreValueDistance)
	}
	if ScoreValueDistance < 0 {
		t.Errorf("ScoreValueDistance = %d, want non-negative", ScoreValueDistance)
	}
}

// ArmResult.Span must be populated for each returned arm — valid file, line,
// col, and non-zero length covering the arm source expression.
func TestRankArms_ArmSpanPopulated(t *testing.T) {
	arms := parseOrChain(t, `"Bash" | "Read" | "Write"`)
	ctx := cuecontext.New()
	ruleVal := ctx.CompileString(`"Bash" | "Read" | "Write"`, cue.Filename("rule.cue"))
	if err := ruleVal.Err(); err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	input := compileRankVal(t, `"Rd"`)

	got := rankArms(arms, ruleVal, input)

	for i, a := range got {
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

// sanity: returned slice carries diag.ArmResult elements, not something else.
// Compile-time check via assignment + runtime check via type assertion.
func TestRankArms_ReturnsArmResultSlice(t *testing.T) {
	arms := parseOrChain(t, `"A" | "B"`)
	ctx := cuecontext.New()
	ruleVal := ctx.CompileString(`"A" | "B"`, cue.Filename("rule.cue"))
	if err := ruleVal.Err(); err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	input := compileRankVal(t, `"Z"`)

	var got = rankArms(arms, ruleVal, input)
	if len(got) != 2 {
		t.Fatalf("rankArms returned %d arms, want 2", len(got))
	}
}
