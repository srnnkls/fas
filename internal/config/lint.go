package config

import (
	"errors"

	"cuelang.org/go/cue/ast"

	"github.com/srnnkls/fas/internal/diag"
)

// lintRulePackage walks every top-level rule's `when` subtree across all files
// of the merged package and rejects three reference patterns that survive CUE
// compilation but cannot express legitimate author intent:
//
//  1. Cross-rule refs — a rule's `when` reaches into another top-level rule's
//     `when`, `then`, or `meta` subtree via a selector expression. Now detected
//     across files because the rule-name set is the union over the package.
//     Emits E0502.
//  2. Self-refs into `then`/`meta` — a rule's `when` reaches into its own
//     `then` or `meta` subtree. Those fields are not available at match time;
//     `when` must be a pure pattern over the input. Emits E0503.
//  3. Unbound identifiers — an identifier that resolves to none of: a local
//     binding, a stdlib import, a predeclared builtin, a package-wide hidden
//     helper/definition, or a package-wide rule. Emits E0501.
//
// Each violation is reported as a *diag.DiagError so downstream callers can
// recover the structured Diagnostic via errors.As while the rendered error
// string keeps the taxonomy terms existing regression tests grep for. Each
// diagnostic anchors at a node from its originating file's AST, so per-file
// positions attribute the error to the file the offending `when` lives in.
func lintRulePackage(origins []fileOrigin) error {
	ruleNames := make(map[string]struct{})
	helperDefNames := make(map[string]struct{})
	for _, o := range origins {
		if o.file == nil {
			continue
		}
		collectTopLevelNames(o.file, ruleNames, helperDefNames)
	}

	var errs []error
	for _, o := range origins {
		if o.file == nil {
			continue
		}
		for _, decl := range o.file.Decls {
			field, ok := decl.(*ast.Field)
			if !ok {
				continue
			}
			name, isRule := ruleLabelName(field)
			if !isRule {
				continue
			}
			whenExpr := findWhenExpr(field.Value)
			if whenExpr == nil {
				continue
			}
			if err := lintWhen(name, ruleNames, helperDefNames, whenExpr); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// collectTopLevelNames adds each file's top-level labels to the package-wide
// sets: rule-shaped fields (ident or quoted, per ruleLabelName) to ruleNames,
// and hidden `_x` helpers / `#X` definitions to helperDefNames.
func collectTopLevelNames(file *ast.File, ruleNames, helperDefNames map[string]struct{}) {
	for _, decl := range file.Decls {
		field, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		if name, isRule := ruleLabelName(field); isRule {
			ruleNames[name] = struct{}{}
			continue
		}
		name, isIdent, err := ast.LabelName(field.Label)
		if err != nil || !isIdent || name == "" {
			continue
		}
		switch name[0] {
		case '_', '#':
			helperDefNames[name] = struct{}{}
		}
	}
}

// isExportedOrRegular reports whether a top-level label names a regular field
// (not a hidden `_foo` helper and not a `#Def` definition).
func isExportedOrRegular(name string) bool {
	if name == "" {
		return false
	}
	switch name[0] {
	case '_', '#':
		return false
	}
	return true
}

// findWhenExpr extracts the value expression of the `when` field from a rule's
// struct literal, or nil if the rule does not declare `when`.
func findWhenExpr(value ast.Expr) ast.Expr {
	strct, ok := value.(*ast.StructLit)
	if !ok {
		return nil
	}
	for _, decl := range strct.Elts {
		field, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		name, isIdent, err := ast.LabelName(field.Label)
		if err != nil || !isIdent {
			continue
		}
		if name == "when" {
			return field.Value
		}
	}
	return nil
}

// lintWhen walks whenExpr and classifies every identifier reference it finds.
// Returns the first violation as a *diag.DiagError or nil if the subtree is
// clean.
//
// The walk is hand-rolled rather than ast.Walk-based because field LABELS are
// also ast.Ident nodes — a naive visitor would misclassify the LHS of
// `tool_name: "Bash"` as a value reference. This walker descends into Field
// values but never into their labels; similarly, LetClause idents, ForClause
// key/value idents, Alias target idents, and ImportSpec names are binding
// positions rather than references.
func lintWhen(ruleName string, ruleNames, helperDefNames map[string]struct{}, whenExpr ast.Expr) error {
	var firstErr error
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		if firstErr != nil || n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if err := checkSelector(ruleName, ruleNames, helperDefNames, node); err != nil {
				firstErr = err
				return
			}
			// Do NOT descend into the root ident via the default walk;
			// checkSelector already classified it. Descending would re-visit
			// `rule_one` as a bare ident and miss the selector-path context.
			return
		case *ast.Ident:
			if err := checkIdent(ruleName, ruleNames, helperDefNames, node); err != nil {
				firstErr = err
				return
			}
		case *ast.Field:
			// Skip node.Label (binding position); descend only into the value.
			walk(node.Value)
			return
		case *ast.LetClause:
			walk(node.Expr)
			return
		case *ast.ForClause:
			walk(node.Source)
			return
		case *ast.Alias:
			walk(node.Expr)
			return
		case *ast.ImportSpec:
			return
		case *ast.StructLit:
			for _, elt := range node.Elts {
				walk(elt)
			}
			return
		case *ast.ListLit:
			for _, elt := range node.Elts {
				walk(elt)
			}
			return
		case *ast.ParenExpr:
			walk(node.X)
			return
		case *ast.UnaryExpr:
			walk(node.X)
			return
		case *ast.BinaryExpr:
			walk(node.X)
			walk(node.Y)
			return
		case *ast.IndexExpr:
			walk(node.X)
			walk(node.Index)
			return
		case *ast.SliceExpr:
			walk(node.X)
			walk(node.Low)
			walk(node.High)
			return
		case *ast.CallExpr:
			walk(node.Fun)
			for _, arg := range node.Args {
				walk(arg)
			}
			return
		case *ast.Interpolation:
			for _, elt := range node.Elts {
				walk(elt)
			}
			return
		case *ast.Comprehension:
			for _, clause := range node.Clauses {
				walk(clause)
			}
			walk(node.Value)
			return
		case *ast.IfClause:
			walk(node.Condition)
			return
		case *ast.EmbedDecl:
			walk(node.Expr)
			return
		case *ast.Ellipsis:
			walk(node.Type)
			return
		}
	}
	walk(whenExpr)
	return firstErr
}

// checkSelector classifies a selector expression whose root is an identifier.
// Cross-rule and self-ref-into-then/meta are the two rejection paths; anything
// else (import paths, local helpers, siblings) falls through to checkIdent on
// the root.
func checkSelector(ruleName string, ruleNames, helperDefNames map[string]struct{}, sel *ast.SelectorExpr) error {
	path := selectorPath(sel)
	if path == nil {
		return nil
	}
	root := path[0]
	rootIdent, ok := root.(*ast.Ident)
	if !ok {
		// The root is an expression (parenthesised or computed). Walk will
		// descend and classify constituent identifiers individually.
		return nil
	}

	// Imports are always in-scope; the selector path enumerates package
	// members which the lint does not inspect.
	if isImportRef(rootIdent) {
		return nil
	}

	if _, isRule := ruleNames[rootIdent.Name]; isRule {
		// Top-level rule field reached through a selector — classify by the
		// first segment of the path after the root.
		if len(path) < 2 {
			return nil
		}
		first := selectorSegmentName(path[1])
		switch first {
		case "when", "then", "meta":
			// fallthrough to violation dispatch below
		default:
			return nil
		}
		if rootIdent.Name == ruleName {
			// Self-ref — only `then` and `meta` are rejected; self-refs into
			// the rule's own `when` wrap back around to the pattern root and
			// are vacuous rather than harmful.
			if first == "then" || first == "meta" {
				return selfRefDiag(ruleName, first, rootIdent)
			}
			return nil
		}
		return crossRuleDiag(ruleName, rootIdent.Name, first, rootIdent)
	}

	// Root is not a rule, not an import — classify it as a bare ident would be.
	return checkIdent(ruleName, ruleNames, helperDefNames, rootIdent)
}

// checkIdent classifies a bare identifier reference. Returns a *diag.DiagError
// for an unbound ident that is none of: a local binding, a stdlib import, a
// predeclared builtin, a package-wide hidden helper/definition, or a
// package-wide rule.
func checkIdent(ruleName string, ruleNames, helperDefNames map[string]struct{}, id *ast.Ident) error {
	// The parser resolves idents against file and struct scopes; a nil Node
	// means the reference escapes all scopes visible within its own file. A
	// sibling-file helper/#def or bare rule is unresolved here yet legal once
	// the package merges, so the package-wide sets below admit it.
	if id.Node != nil {
		return nil
	}
	name := id.Name
	if _, isHelperDef := helperDefNames[name]; isHelperDef {
		return nil
	}
	if _, isRule := ruleNames[name]; isRule {
		// Bare top-level rule name used as a value — not a selector ref, so
		// it cannot reach into `then`/`meta`. Treat as allowed; the evaluator
		// will see a struct value and unify normally.
		return nil
	}
	// Predeclared identifiers (string, int, bool, etc.) and top-level builtins
	// have IsPredeclared() true. Let them through.
	if id.IsPredeclared() {
		return nil
	}
	return unboundDiag(ruleName, id)
}

// crossRuleDiag builds an E0502 DiagError whose primary span anchors at the
// selector's root ident — `base_rule` in `base_rule.when.tool_name`. The
// rendered body retains the taxonomy terms ("cross", both rule names) the
// existing loader_test.go substring assertions look for.
//
// Both rule names live in the caret label so they survive
// errDetailAfter's anchor at the file path: the helper returns the suffix
// starting at the `--> path` line, dropping the diagnostic title that appears
// above it.
func crossRuleDiag(ruleName, otherRule, subtree string, root *ast.Ident) error {
	d := diag.Diagnostic{
		Code:     diag.E0502.Code,
		Severity: diag.SeverityError,
		Title: "rule " + quote(ruleName) + ": cross-rule reference into " +
			quote(otherRule) + "." + subtree + " from `when`",
		Primary: diag.Label{
			Pos: root.Pos(),
			Len: len(root.Name),
			Msg: "cross-rule reference from " + quote(ruleName) + " to " +
				quote(otherRule) + "." + subtree,
		},
		Help: diag.E0502.Help,
	}
	return diag.NewDiagError(d, nil, nil)
}

// selfRefDiag builds an E0503 DiagError whose primary span anchors at the
// selector root ident (the rule's own name). The rendered body contains both
// "self" and the offending subtree name ("then" or "meta") so existing
// substring regressions still pass.
func selfRefDiag(ruleName, subtree string, root *ast.Ident) error {
	d := diag.Diagnostic{
		Code:     diag.E0503.Code,
		Severity: diag.SeverityError,
		Title: "rule " + quote(ruleName) + ": `when` refers to its own `" + subtree +
			"` subtree; `then`/`meta` are not visible at match time",
		Primary: diag.Label{
			Pos: root.Pos(),
			Len: len(root.Name),
			Msg: "self-reference into `" + subtree + "`",
		},
		Help: diag.E0503.Help,
	}
	return diag.NewDiagError(d, nil, nil)
}

// unboundDiag builds an E0501 DiagError whose primary span anchors at the
// offending identifier. Help mentions the two documented escape hatches —
// hidden siblings and stdlib imports — so the lint_diag test substring checks
// (`hidden` + `stdlib` / `import`) find them. The rule name sits in the caret
// label so it survives errDetailAfter's file-path anchor.
func unboundDiag(ruleName string, id *ast.Ident) error {
	d := diag.Diagnostic{
		Code:     diag.E0501.Code,
		Severity: diag.SeverityError,
		Title: "rule " + quote(ruleName) + ": unbound identifier " + quote(id.Name) +
			" in `when`",
		Primary: diag.Label{
			Pos: id.Pos(),
			Len: len(id.Name),
			Msg: "unbound identifier " + quote(id.Name) + " in rule " + quote(ruleName),
		},
		Help: "Declare a hidden sibling (leading underscore, e.g. `_" + id.Name +
			": ...`) on the same rule, or import the value from a stdlib package " +
			"(e.g. `import \"list\"`). Bare identifiers in `when` must resolve to " +
			"one of those two scopes.",
	}
	return diag.NewDiagError(d, nil, nil)
}

// quote wraps s in double quotes without escaping; identifier and rule names
// never contain quote-sensitive characters, so strconv.Quote would only add
// noise to the rendered diagnostic.
func quote(s string) string { return "\"" + s + "\"" }

// isImportRef reports whether an identifier was resolved to an import spec by
// the parser. Import aliases are the only builtin binding shape inside a rule
// file; everything else either resolves to a struct field or is unbound.
func isImportRef(id *ast.Ident) bool {
	_, ok := id.Node.(*ast.ImportSpec)
	return ok
}

// selectorPath flattens a SelectorExpr chain into its components in
// left-to-right order. The root expression comes first, followed by each
// selector label in turn.
//
//	rule_one.when.tool_name  →  [Ident("rule_one"), "when", "tool_name"]
func selectorPath(sel *ast.SelectorExpr) []ast.Node {
	var reversed []ast.Node
	reversed = append(reversed, sel.Sel)
	var cur = sel.X
	for {
		s, ok := cur.(*ast.SelectorExpr)
		if !ok {
			break
		}
		reversed = append(reversed, s.Sel)
		cur = s.X
	}
	// Final root — whatever wasn't a SelectorExpr.
	path := make([]ast.Node, 0, len(reversed)+1)
	path = append(path, cur)
	for i := len(reversed) - 1; i >= 0; i-- {
		path = append(path, reversed[i])
	}
	return path
}

// selectorSegmentName extracts the textual name of a selector segment. Only
// identifier and string-label selectors carry names the lint cares about;
// other forms (interpolations, expressions) are opaque.
func selectorSegmentName(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Name
	case ast.Label:
		name, _, err := ast.LabelName(v)
		if err != nil {
			return ""
		}
		return name
	}
	return ""
}
