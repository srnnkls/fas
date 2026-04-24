package evaluator

import (
	"fmt"
	"iter"
	"slices"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/token"

	"github.com/srnnkls/quae/internal/config"
	"github.com/srnnkls/quae/internal/diag"
)

// localize walks the rule's `when` AST paired with the input value and yields
// a diagnostic for each observed mismatch (absent path segment, failed leaf
// constraint, all-arms-fail disjunction). Yielding is lazy — callers that
// break after the first diagnostic halt the walker cleanly.
func localize(rule config.Rule, input cue.Value) iter.Seq[diag.Diagnostic] {
	return func(yield func(diag.Diagnostic) bool) {
		if rule.WhenSyntax == nil {
			return
		}
		walkStruct(rule.WhenSyntax, rule.When, input, nil, yield)
	}
}

// walkStruct recurses over a struct literal paired with the rule and input
// values at the same path. It returns false to propagate a caller-initiated
// break up the recursion stack.
func walkStruct(node ast.Expr, ruleCur, inputCur cue.Value, path []string, yield func(diag.Diagnostic) bool) bool {
	st, ok := node.(*ast.StructLit)
	if !ok {
		return true
	}
	for _, decl := range st.Elts {
		f, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		name := fieldName(f.Label)
		if name == "" {
			continue
		}
		next := inputCur.LookupPath(cue.MakePath(cue.Str(name)))
		if !next.Exists() {
			// Absent optional fields are a match, not a miss.
			if f.Constraint == token.OPTION {
				continue
			}
			if !yield(absentKeyDiagnostic(f, name, inputCur, path)) {
				return false
			}
			continue
		}

		ruleNext := ruleCur.LookupPath(cue.MakePath(cue.Str(name)))
		if inner, ok := f.Value.(*ast.StructLit); ok {
			if !walkStruct(inner, ruleNext, next, append(path, name), yield) {
				return false
			}
			continue
		}

		// Leaf value — skip when there is no rule-side constraint (the
		// field exists in input but has no corresponding leaf in the
		// rule, e.g. struct-shaped rule slot with extra input keys).
		if !ruleNext.Exists() {
			continue
		}
		// Short-circuit the leaf so no second diagnostic follows.
		if kindsDisjoint(ruleNext.IncompleteKind(), next.Kind()) {
			if !yield(kindMismatchDiagnostic(f, ruleNext, next)) {
				return false
			}
			continue
		}
		if ruleNext.Subsume(next, cue.Final(), cue.Schema()) == nil {
			continue
		}
		if b, ok := f.Value.(*ast.BinaryExpr); ok && b.Op == token.OR {
			if !yield(disjunctionDiagnostic(b, next)) {
				return false
			}
			continue
		}
		if !yield(leafDiagnostic(f, next)) {
			return false
		}
	}
	return true
}

// absentKeyDiagnostic builds an E0201 carrying a caret at the field label and
// a help line listing the keys the input actually exposes at the parent path.
func absentKeyDiagnostic(f *ast.Field, name string, parent cue.Value, path []string) diag.Diagnostic {
	available := listKeys(parent)
	if available == nil {
		available = []string{}
	}
	suggestion := ""
	if len(available) > 0 {
		bestIdx, bestDist := 0, levenshtein(name, available[0])
		for i := 1; i < len(available); i++ {
			d := levenshtein(name, available[i])
			if d < bestDist {
				bestDist = d
				bestIdx = i
			}
		}
		if bestDist <= 2 {
			suggestion = available[bestIdx]
		}
	}
	return diag.Diagnostic{
		Code:     diag.E0201.Code,
		Severity: diag.SeverityError,
		Title:    "key not found",
		Primary: diag.Label{
			Pos: f.Label.Pos(),
			Len: len(name),
			Msg: fmt.Sprintf("key %q not found in input at path %s", name, joinPath(path)),
			Reasons: []diag.Reason{
				diag.KeyMissing{
					Key:           name,
					AvailableKeys: available,
					Suggestion:    suggestion,
				},
			},
		},
		Help: fmt.Sprintf("input.%s has keys: %s", joinPath(path), strings.Join(available, ", ")),
	}
}

// leafDiagnostic builds an E0301 pairing the constraint expression and the
// actual input value via `want:` / `got:` notes. Tokens are exact because
// downstream tooling greps for them.
func leafDiagnostic(f *ast.Field, actual cue.Value) diag.Diagnostic {
	wantStr := renderExpr(f.Value)
	gotStr := renderValue(actual)
	span := exprLen(f.Value)
	return diag.Diagnostic{
		Code:     diag.E0301.Code,
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: f.Value.Pos(),
			Len: span,
			Msg: "constraint not satisfied",
		},
		Notes: []diag.Label{
			{Pos: f.Value.Pos(), Len: span, Msg: "want: " + wantStr},
			{Pos: f.Value.Pos(), Len: span, Msg: "got: " + gotStr},
		},
	}
}

// kindsDisjoint reports whether two kind masks share no lattice overlap.
// Number-family bits (IntKind, FloatKind, NumberKind) are treated as mutually
// overlapping so refinements like `int` vs a `5.5` input fall through to the
// Subsume path rather than surfacing as a type mismatch.
func kindsDisjoint(want, got cue.Kind) bool {
	// bottom on either side: defer to Subsume.
	if want == 0 || got == 0 {
		return false
	}
	if want&got != 0 {
		return false
	}
	const numberFamily = cue.IntKind | cue.FloatKind | cue.NumberKind
	if want&numberFamily != 0 && got&numberFamily != 0 {
		return false
	}
	return true
}

// kindMismatchDiagnostic builds an E0303 with a singular KindMismatch Reason.
func kindMismatchDiagnostic(f *ast.Field, ruleNext, actual cue.Value) diag.Diagnostic {
	return diag.Diagnostic{
		Code:     diag.E0303.Code,
		Severity: diag.SeverityError,
		Title:    "type mismatch",
		Primary: diag.Label{
			Pos: f.Value.Pos(),
			Len: exprLen(f.Value),
			Reasons: []diag.Reason{
				diag.KindMismatch{
					Want:   ruleNext.IncompleteKind(),
					Got:    actual.Kind(),
					Actual: renderValue(actual),
				},
			},
		},
	}
}

// disjunctionDiagnostic builds an E0401 spanning the entire `A | B | C` chain
// with one Note per arm so the renderer can underline each alternative.
func disjunctionDiagnostic(expr *ast.BinaryExpr, actual cue.Value) diag.Diagnostic {
	arms := flattenOrArms(expr)
	notes := make([]diag.Label, 0, len(arms))
	for _, arm := range arms {
		notes = append(notes, diag.Label{
			Pos: arm.Pos(),
			Len: exprLen(arm),
			Msg: fmt.Sprintf("arm %s did not match", renderExpr(arm)),
		})
	}
	return diag.Diagnostic{
		Code:     diag.E0401.Code,
		Severity: diag.SeverityError,
		Title:    "no disjunction arm matched",
		Primary: diag.Label{
			Pos: expr.Pos(),
			Len: exprLen(expr),
			Msg: "no arm subsumes " + renderValue(actual),
		},
		Notes: notes,
	}
}

// fieldName extracts the textual name from a field label, supporting the
// identifier and quoted-string forms the CUE parser emits.
func fieldName(l ast.Label) string {
	switch x := l.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.BasicLit:
		s := x.Value
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			return s[1 : len(s)-1]
		}
		return s
	}
	return ""
}

// joinPath renders an accumulated walker path for use in diagnostic messages.
// An empty path renders as "<root>" so the message always has something
// concrete to point at.
func joinPath(path []string) string {
	if len(path) == 0 {
		return "<root>"
	}
	return strings.Join(path, ".")
}

// listKeys returns the sorted field names of v, or an empty slice if v is not
// a struct. Sorting keeps diagnostic output deterministic across runs.
func listKeys(v cue.Value) []string {
	it, err := v.Fields(cue.Optional(true), cue.Definitions(false), cue.Hidden(false))
	if err != nil {
		return nil
	}
	var keys []string
	for it.Next() {
		sel := it.Selector()
		if sel.LabelType() == cue.StringLabel {
			keys = append(keys, sel.Unquoted())
			continue
		}
		keys = append(keys, sel.String())
	}
	slices.Sort(keys)
	return keys
}

// flattenOrArms unwinds a left-associative `A | B | C` chain into [A, B, C] so
// per-arm labels can carry their own positions.
func flattenOrArms(expr *ast.BinaryExpr) []ast.Expr {
	var arms []ast.Expr
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.OR {
			walk(b.X)
			walk(b.Y)
			return
		}
		arms = append(arms, e)
	}
	walk(expr)
	return arms
}

// renderExpr formats a CUE AST expression back to source form. The CUE
// formatter preserves the original spelling when possible.
func renderExpr(e ast.Expr) string {
	b, err := format.Node(e)
	if err != nil {
		return fmt.Sprintf("<unformatted %T>", e)
	}
	return strings.TrimSpace(string(b))
}

// renderValue formats an evaluated CUE value so it can appear on the `got:`
// line. Strings are rendered with their surrounding quotes preserved so the
// reader can distinguish "true" from true at a glance.
func renderValue(v cue.Value) string {
	node := v.Syntax(cue.Final(), cue.Concrete(true))
	if node == nil {
		return fmt.Sprintf("%v", v)
	}
	b, err := format.Node(node)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSpace(string(b))
}

// exprLen returns the byte span of e in the source file, falling back to 1 so
// the renderer always has at least one column to underline.
func exprLen(e ast.Expr) int {
	start, end := e.Pos(), e.End()
	if !start.IsValid() || !end.IsValid() {
		return 1
	}
	n := end.Offset() - start.Offset()
	if n <= 0 {
		return 1
	}
	return n
}
