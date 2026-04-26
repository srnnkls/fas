package evaluator

import (
	"math"
	"sort"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/diag"
)

// Score tier constants per AD-3. Strictly monotonic so a kind match always
// outranks any structural-only score, which always outranks any pure value
// distance bonus. Pinned by tests in disjunction_rank_test.go.
const (
	ScoreKindMatch       = 100
	ScoreStructuralMatch = 50
	ScoreValueDistance   = 1
)

// hasConcreteArm reports whether the resolved disjunction value has at least
// one arm whose Kind() is concrete (literal, not abstract type). Pure
// kind-union arms (`int | string`) fail this check, signaling that the
// caller should defer to the KindMismatch path on disjoint inputs.
func hasConcreteArm(v cue.Value) bool {
	op, operands := v.Expr()
	if op != cue.OrOp {
		return v.Kind() != cue.BottomKind
	}
	for _, o := range operands {
		if o.Kind() != cue.BottomKind {
			return true
		}
	}
	return false
}

// rankArms scores every arm of a failed disjunction against the input value
// and returns []diag.ArmResult sorted by Score descending; ties break by
// source order via stable sort. Inner is left nil — T12 owns per-arm sub-
// reason population for the renderer.
func rankArms(arms []ast.Expr, ruleVal cue.Value, input cue.Value) []diag.ArmResult {
	_ = ruleVal
	ctx := cuecontext.New()
	inputKind := concreteOrIncompleteKind(input)

	results := make([]diag.ArmResult, 0, len(arms))
	for _, arm := range arms {
		armVal := ctx.BuildExpr(arm)
		rendered := renderExpr(arm)
		results = append(results, diag.ArmResult{
			Arm:   rendered,
			Span:  armSpan(arm, rendered),
			Inner: nil,
			Score: scoreArm(armVal, input, inputKind),
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// rankArmValues is the cue.Value-input variant of rankArms, used when the
// disjunction is reached via a reference (Ident / SelectorExpr) rather than
// as a literal BinaryExpr on the field. armVals come straight from
// ruleNext.Expr() so each arm's Pos() points at its definition site —
// possibly in a different file from f.Value. Span/score model is otherwise
// identical to the AST path.
func rankArmValues(armVals []cue.Value, input cue.Value, render func(cue.Value) string) []diag.ArmResult {
	inputKind := concreteOrIncompleteKind(input)
	results := make([]diag.ArmResult, 0, len(armVals))
	for _, armVal := range armVals {
		rendered := render(armVal)
		results = append(results, diag.ArmResult{
			Arm:   rendered,
			Span:  cueValueSpan(armVal, rendered),
			Inner: nil,
			Score: scoreArm(armVal, input, inputKind),
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// cueValueSpan derives a serializable Span from a cue.Value's source
// position. Length is the rendered form's byte width (≥ 1) since cue.Value
// has no End()-equivalent for arbitrary expressions. Falls back to a
// position-less Span when Pos() is invalid.
func cueValueSpan(v cue.Value, rendered string) diag.Span {
	pos := v.Pos()
	length := len(rendered)
	if length <= 0 {
		length = 1
	}
	if !pos.IsValid() {
		return diag.Span{Length: length}
	}
	return diag.Span{
		File:   pos.Filename(),
		Line:   pos.Line(),
		Col:    pos.Column(),
		Length: length,
	}
}

// scoreArm computes the closeness score of a single arm against the input.
// Returns 0 when no kind overlap exists; otherwise base ScoreKindMatch plus
// optional structural and value-distance bonuses per the tier table.
func scoreArm(armVal, input cue.Value, inputKind cue.Kind) int {
	armKind := concreteOrIncompleteKind(armVal)
	if armKind&inputKind == 0 {
		return 0
	}
	score := ScoreKindMatch

	if armKind&cue.StructKind != 0 && inputKind&cue.StructKind != 0 {
		score += structuralBonus(armVal, input)
	}
	if armVal.Kind() != cue.BottomKind && armKind&cue.StructKind == 0 {
		score += valueDistanceBonus(armVal, input)
	}
	return score
}

// structuralBonus rewards struct arms whose field set overlaps the input's.
// The base term is ScoreStructuralMatch * (overlap / max(armFields, inputFields));
// each overlapping field whose nested kind also matches the input's adds an
// additional ScoreValueDistance so e.g. {a: string} vs {a: "x"} scores
// strictly higher than {a: int} vs {a: "x"}.
func structuralBonus(armVal, input cue.Value) int {
	armFields := structFieldKinds(armVal)
	inputFields := structFieldKinds(input)
	if len(armFields) == 0 || len(inputFields) == 0 {
		return 0
	}
	overlap := 0
	kindMatchInner := 0
	for name, ak := range armFields {
		ik, ok := inputFields[name]
		if !ok {
			continue
		}
		overlap++
		if ak&ik != 0 {
			kindMatchInner++
		}
	}
	if overlap == 0 {
		return 0
	}
	maxFields := max(len(inputFields), len(armFields))
	return ScoreStructuralMatch*overlap/maxFields + kindMatchInner*ScoreValueDistance
}

// valueDistanceBonus returns ScoreValueDistance minus a kind-aware distance
// between the concrete arm value and the input. Higher = closer; exact match
// scores ScoreValueDistance, large divergences go negative so closer arms
// always sort above farther ones at the same kind tier.
func valueDistanceBonus(armVal, input cue.Value) int {
	armKind := armVal.Kind()
	switch {
	case armKind&cue.StringKind != 0:
		armStr, aerr := armVal.String()
		inStr, ierr := input.String()
		if aerr != nil || ierr != nil {
			return 0
		}
		return ScoreValueDistance - levenshtein(armStr, inStr)
	case armKind&(cue.IntKind|cue.FloatKind|cue.NumberKind) != 0:
		armF, aerr := numericFloat(armVal)
		inF, ierr := numericFloat(input)
		if aerr != nil || ierr != nil {
			return 0
		}
		delta := math.Abs(armF - inF)
		const distanceCap = 1000.0
		if delta > distanceCap {
			delta = distanceCap
		}
		return ScoreValueDistance - int(delta)
	}
	return 0
}

// concreteOrIncompleteKind prefers the concrete Kind() when available and
// falls back to IncompleteKind() so abstract type arms (e.g. `int`, `string`)
// still report a meaningful kind for tier matching.
func concreteOrIncompleteKind(v cue.Value) cue.Kind {
	if k := v.Kind(); k != cue.BottomKind {
		return k
	}
	return v.IncompleteKind()
}

// structFieldKinds returns a name→kind map of v's top-level fields, treating
// abstract field types (e.g. `string`) via IncompleteKind so disjunction
// scoring can compare schema arms against concrete inputs.
func structFieldKinds(v cue.Value) map[string]cue.Kind {
	it, err := v.Fields(cue.Optional(true), cue.Definitions(false), cue.Hidden(false))
	if err != nil {
		return nil
	}
	out := map[string]cue.Kind{}
	for it.Next() {
		out[it.Selector().String()] = concreteOrIncompleteKind(it.Value())
	}
	return out
}

// numericFloat extracts a float64 from any concrete numeric value (int or
// float), so the distance bonus can compare across the IntKind/FloatKind
// boundary uniformly.
func numericFloat(v cue.Value) (float64, error) {
	if f, err := v.Float64(); err == nil {
		return f, nil
	}
	i, err := v.Int64()
	if err != nil {
		return 0, err
	}
	return float64(i), nil
}

// armSpan derives a serializable Span from an arm's source position. Falls
// back to the arm node's End() offset when length cannot be inferred from the
// rendered form, preserving Length > 0 for downstream renderers.
func armSpan(arm ast.Expr, rendered string) diag.Span {
	pos := arm.Pos()
	if !pos.IsValid() {
		return diag.Span{Length: len(rendered)}
	}
	length := len(rendered)
	if end := arm.End(); end.IsValid() {
		if n := end.Offset() - pos.Offset(); n > 0 {
			length = n
		}
	}
	if length <= 0 {
		length = 1
	}
	return diag.Span{
		File:   pos.Filename(),
		Line:   pos.Line(),
		Col:    pos.Column(),
		Length: length,
	}
}
