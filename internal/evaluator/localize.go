package evaluator

import (
	"fmt"
	"iter"
	"slices"
	"strconv"
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
		// Literal-arm disjunctions own their own narrative via ranked arms
		// (E0401 + DisjunctionFailed). Pure kind-union disjunctions
		// (`int | string`) defer to the leaf-level KindMismatch path so
		// "expected int|string, got bool" stays a single E0303 reason
		// rather than three useless arm rankings.
		if b, ok := f.Value.(*ast.BinaryExpr); ok && b.Op == token.OR && hasConcreteArm(ruleNext) {
			if ruleNext.Subsume(next, cue.Final(), cue.Schema()) == nil {
				continue
			}
			if !yield(disjunctionDiagnostic(b, ruleNext, next)) {
				return false
			}
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
		reasons := failingConjuncts(ruleNext, next)
		if !yield(leafDiagnostic(f, ruleNext, reasons)) {
			return false
		}
	}
	return true
}

// flattenConjuncts recurses into v.Expr() and returns the leaf operands of any
// top-level `&` chain in source order. `(A & B) & C` flattens to `[A, B, C]`.
// A value whose top-level op is not AndOp contributes itself as a single entry.
func flattenConjuncts(v cue.Value) []cue.Value {
	op, operands := v.Expr()
	if op == cue.AndOp {
		out := make([]cue.Value, 0, len(operands))
		for _, o := range operands {
			out = append(out, flattenConjuncts(o)...)
		}
		return out
	}
	return []cue.Value{v}
}

// failingConjuncts walks ruleNext's `&` chain and returns one ConjunctFailed
// entry per operand that does not subsume input, preserving source order.
// Returns nil when ruleNext has fewer than two conjuncts so a literal leaf
// falls through to the legacy Msg path (NF5). When a failing conjunct is a
// bound expression (op >=, <=, >, <, !=), Sub carries a BoundViolation.
func failingConjuncts(ruleNext, input cue.Value) []diag.Reason {
	conjuncts := flattenConjuncts(ruleNext)
	if len(conjuncts) < 2 {
		return nil
	}
	var reasons []diag.Reason
	for _, c := range conjuncts {
		if c.Subsume(input, cue.Final(), cue.Schema()) == nil {
			continue
		}
		expr, span := conjunctExprAndSpan(c)
		reasons = append(reasons, diag.ConjunctFailed{
			Expr: expr,
			Span: span,
			Sub:  conjunctSubReason(c, input),
		})
	}
	return reasons
}

// conjunctSubReason inspects a failing conjunct's source AST and returns a
// structured Reason describing the shape of the failure when it matches a
// recognised form. Handles bound expressions (op >=, <=, >, <, !=) yielding
// BoundViolation, and regex matches (op =~) yielding RegexMismatch. Returns
// nil for shapes not yet specialised so v0 fallback rendering applies.
func conjunctSubReason(c, input cue.Value) diag.Reason {
	u, ok := c.Source().(*ast.UnaryExpr)
	if !ok {
		return nil
	}
	if opStr, isBound := boundOpString(u.Op); isBound {
		bound := strings.TrimSpace(renderExpr(u.X))
		actual := renderValue(input)
		return diag.BoundViolation{
			Op:       opStr,
			Bound:    bound,
			Actual:   actual,
			Distance: boundDistance(u.Op, bound, actual),
		}
	}
	if u.Op == token.MAT {
		return regexSubReason(u.X, input)
	}
	return nil
}

// regexSubReason extracts the pattern string from a =~ operand and the input
// string from the failing input value, then computes DivergeAt via
// regex_diverge. Returns a RegexMismatch reason even when pattern extraction
// partially fails or divergence is unavailable — the renderer falls back on
// DivergeAt=-1 uniformly. Returns nil only when neither pattern nor input
// can be recovered at all (so v0 Msg rendering applies).
func regexSubReason(patternExpr ast.Expr, input cue.Value) diag.Reason {
	lit, ok := patternExpr.(*ast.BasicLit)
	if !ok {
		return nil
	}
	pattern, err := strconv.Unquote(lit.Value)
	if err != nil {
		return nil
	}
	inputStr, err := input.String()
	if err != nil {
		return nil
	}
	return diag.RegexMismatch{
		Pattern:   pattern,
		Input:     inputStr,
		DivergeAt: regexDiverge(pattern, inputStr),
	}
}

// boundOpString maps a CUE bound token to its canonical literal form. The
// bool result signals whether the token is one of the five bound operators.
func boundOpString(op token.Token) (string, bool) {
	switch op {
	case token.GEQ:
		return ">=", true
	case token.LEQ:
		return "<=", true
	case token.GTR:
		return ">", true
	case token.LSS:
		return "<", true
	case token.NEQ:
		return "!=", true
	}
	return "", false
}

// boundDistance pre-formats the "off by N" distance string per spec:
//   - numeric ≥/≤/>/<: absolute value of (actual − bound), formatted with %g.
//   - strict > or < where actual == bound: "off by 1" (smallest violation).
//   - != with actual == bound: "" (no scalar distance for equality failure).
//   - non-numeric operands: "".
func boundDistance(op token.Token, boundStr, actualStr string) string {
	if op == token.NEQ {
		return ""
	}
	bound, berr := strconv.ParseFloat(boundStr, 64)
	actual, aerr := strconv.ParseFloat(actualStr, 64)
	if berr != nil || aerr != nil {
		return ""
	}
	delta := actual - bound
	if delta < 0 {
		delta = -delta
	}
	// Strict inequality with actual == bound: smallest scalar violation is 1.
	if delta == 0 && (op == token.GTR || op == token.LSS) {
		return "off by 1"
	}
	return "off by " + strconv.FormatFloat(delta, 'g', -1, 64)
}

// conjunctExprAndSpan renders a conjunct value's source expression and builds
// a serializable Span DTO from its Source() position. Span.Length matches the
// rendered expression's byte length so downstream renderers underline exactly
// the failing conjunct. Falls back to an empty Span on unresolvable source.
func conjunctExprAndSpan(v cue.Value) (string, diag.Span) {
	src := v.Source()
	if src == nil {
		return renderValue(v), diag.Span{}
	}
	b, err := format.Node(src)
	if err != nil {
		return fmt.Sprintf("<unformatted %T>", src), diag.Span{}
	}
	expr := strings.TrimSpace(string(b))
	pos := src.Pos()
	if !pos.IsValid() {
		return expr, diag.Span{}
	}
	return expr, diag.Span{
		File:   pos.Filename(),
		Line:   pos.Line(),
		Col:    pos.Column(),
		Length: len(expr),
	}
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
		Help: fmt.Sprintf("%s has keys: %s", joinPath(path), strings.Join(available, ", ")),
	}
}

// leafDiagnostic builds an E0301 for a failed leaf constraint. The Primary
// Label carries the structured Reason slice when one was produced (T4/T5/T6)
// and underlines the first failing conjunct; otherwise it carries an empty
// Msg (no "constraint not satisfied" restatement — the Title already says so
// per F7). A conditional `want:` Note is appended only when the emission
// gates in wantNoteMsg fire; the legacy unconditional `want:`/`got:` pair is
// gone. Provenance footer notes (T9) are appended for every cross-file
// conjunct carried by ruleNext, capped at maxProvenanceEntries.
func leafDiagnostic(f *ast.Field, ruleNext cue.Value, reasons []diag.Reason) diag.Diagnostic {
	hostFile := f.Value.Pos().Filename()
	pos := f.Value.Pos()
	span := exprLen(f.Value)
	if len(reasons) > 0 {
		if first, ok := reasons[0].(diag.ConjunctFailed); ok && first.Span.Length > 0 {
			if p := firstConjunctPos(f.Value, first.Span); p.IsValid() {
				pos = p
			}
			span = first.Span.Length
		}
	}
	d := diag.Diagnostic{
		Code:     diag.E0301.Code,
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos:     pos,
			Len:     span,
			Reasons: reasons,
		},
	}
	if msg, ok := wantNoteMsg(f.Value, ruleNext); ok {
		d.Notes = append(d.Notes, diag.Label{
			Pos: f.Value.Pos(),
			Len: exprLen(f.Value),
			Msg: msg,
		})
	}
	d.Notes = append(d.Notes, provenanceNotes(ruleNext, hostFile)...)
	return d
}

// wantNoteMsg decides whether a `want:` Note should accompany a leaf
// diagnostic and, if so, returns the expanded form to display. Per F7 the
// two gates are:
//
//   - Cheap AST gate: f.Value is *ast.Ident or *ast.SelectorExpr — a
//     reference whose source spelling hides the constraint body.
//   - Format-divergence gate: the formatted expanded form of ruleNext
//     differs from the formatted source of f.Value — unification narrowed
//     the constraint beyond what the caret underlines.
//
// When neither fires the caret-underlined span already conveys the
// constraint and a `want:` line would only restate what the reader can see.
func wantNoteMsg(fValue ast.Expr, ruleNext cue.Value) (string, bool) {
	expanded := formattedExpanded(ruleNext)
	if expanded == "" {
		return "", false
	}
	switch fValue.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return "want: " + expanded, true
	}
	if expanded != renderExpr(fValue) {
		return "want: " + expanded, true
	}
	return "", false
}

// formattedExpanded returns the expanded, reference-resolved source form of v
// (e.g. `int & >=0` for a value narrowed from `int` via a stdlib constraint).
// Empty string signals the value's syntax could not be formatted — treat as
// "no useful expanded form available".
func formattedExpanded(v cue.Value) string {
	node := v.Eval().Syntax(cue.Raw())
	if node == nil {
		return ""
	}
	b, err := format.Node(node)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// firstConjunctPos derives a token.Pos anchored at the failing conjunct's
// source location. The renderer needs a token.Pos for file/line/col alignment,
// while the Reason tree carries only a serializable Span DTO — so we reconstruct
// the Pos from the enclosing AST node by matching file+line+column.
func firstConjunctPos(leaf ast.Expr, span diag.Span) token.Pos {
	if span.File == "" || span.Line <= 0 || span.Col <= 0 {
		return token.NoPos
	}
	var found token.Pos
	ast.Walk(leaf, func(n ast.Node) bool {
		if found.IsValid() {
			return false
		}
		p := n.Pos()
		if !p.IsValid() {
			return true
		}
		if p.Filename() == span.File && p.Line() == span.Line && p.Column() == span.Col {
			found = p
			return false
		}
		return true
	}, nil)
	return found
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
// Provenance notes (T9) are appended for every cross-file conjunct of
// ruleNext so cross-package type constraints show their origin.
func kindMismatchDiagnostic(f *ast.Field, ruleNext, actual cue.Value) diag.Diagnostic {
	d := diag.Diagnostic{
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
	d.Notes = append(d.Notes, provenanceNotes(ruleNext, f.Value.Pos().Filename())...)
	return d
}

// disjunctionDiagnostic builds an E0401 spanning the entire `A | B | C` chain
// with a Primary Label carrying a DisjunctionFailed Reason whose Arms are
// ranked by closeness to the input. T12 owns the per-arm caret rendering;
// here we populate only the data layer. Provenance notes (T9) are appended
// for every cross-file arm source so disjunctions composed from stdlib
// imports surface their origin on the footer.
func disjunctionDiagnostic(expr *ast.BinaryExpr, ruleNext, actual cue.Value) diag.Diagnostic {
	arms := flattenOrArms(expr)
	ranked := rankArms(arms, ruleNext, actual)
	d := diag.Diagnostic{
		Code:     diag.E0401.Code,
		Severity: diag.SeverityError,
		Title:    "no disjunction arm matched",
		Primary: diag.Label{
			Pos:     expr.Pos(),
			Len:     exprLen(expr),
			Msg:     "no arm subsumes " + renderValue(actual),
			Reasons: []diag.Reason{diag.DisjunctionFailed{Arms: ranked}},
		},
	}
	d.Notes = append(d.Notes, provenanceNotes(ruleNext, expr.Pos().Filename())...)
	return d
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
