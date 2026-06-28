package evaluator

import (
	"cuelang.org/go/cue"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
)

// bindingFailure describes a single variable whose bound paths resolved to
// different concrete values in the input.
type bindingFailure struct {
	variable string
	paths    []string
	values   []string
}

// checkBindings resolves each @bind variable group against the input and
// verifies that all paths sharing a variable name unify to the same concrete
// value. Returns nil when all bindings are satisfied or when the rule has no
// bindings. Returns the first failing variable group otherwise.
func checkBindings(bindings []config.Binding, input cue.Value) *bindingFailure {
	if len(bindings) == 0 {
		return nil
	}

	groups := groupBindings(bindings)
	for _, g := range groups {
		if len(g.bindings) < 2 {
			continue
		}
		var resolved []cue.Value
		var paths []string
		for _, b := range g.bindings {
			v := resolvePath(input, b)
			if !v.Exists() {
				return &bindingFailure{
					variable: g.variable,
					paths:    bindingPaths(g.bindings),
					values:   []string{"<absent>"},
				}
			}
			resolved = append(resolved, v)
			paths = append(paths, formatBindingPath(b))
		}

		if !allEqual(resolved) {
			values := make([]string, len(resolved))
			for i, v := range resolved {
				values[i] = renderValue(v)
			}
			return &bindingFailure{
				variable: g.variable,
				paths:    paths,
				values:   values,
			}
		}
	}
	return nil
}

// bindingGroup collects all bindings that share a variable name.
type bindingGroup struct {
	variable string
	bindings []config.Binding
}

// groupBindings partitions bindings by variable name, preserving order
// of first occurrence.
func groupBindings(bindings []config.Binding) []bindingGroup {
	idx := map[string]int{}
	var groups []bindingGroup
	for _, b := range bindings {
		if i, ok := idx[b.Variable]; ok {
			groups[i].bindings = append(groups[i].bindings, b)
		} else {
			idx[b.Variable] = len(groups)
			groups = append(groups, bindingGroup{
				variable: b.Variable,
				bindings: []config.Binding{b},
			})
		}
	}
	return groups
}

// resolvePath looks up a binding's path in the input, optionally applying
// the sub-path (e.g., list index) to reach a nested element.
func resolvePath(input cue.Value, b config.Binding) cue.Value {
	selectors := make([]cue.Selector, len(b.FieldPath))
	for i, seg := range b.FieldPath {
		selectors[i] = cue.Str(seg)
	}
	v := input.LookupPath(cue.MakePath(selectors...))
	if !v.Exists() {
		return v
	}
	if idx := b.SubIndex(); idx >= 0 {
		v = v.LookupPath(cue.MakePath(cue.Index(idx)))
	}
	return v
}

// allEqual returns true when every value in vs is pairwise equal under
// mutual subsumption. An empty or single-element slice is trivially equal.
func allEqual(vs []cue.Value) bool {
	if len(vs) < 2 {
		return true
	}
	first := vs[0]
	for _, v := range vs[1:] {
		if first.Subsume(v, cue.Final()) != nil || v.Subsume(first, cue.Final()) != nil {
			return false
		}
	}
	return true
}

// formatBindingPath renders a binding's full resolution path for diagnostics.
func formatBindingPath(b config.Binding) string {
	path := joinDotPath(b.FieldPath)
	if idx := b.SubIndex(); idx >= 0 {
		return path + "[" + b.SubPath + "]"
	}
	if b.SubPath != "" {
		return path + "." + b.SubPath
	}
	return path
}

func joinDotPath(parts []string) string {
	if len(parts) == 0 {
		return "<root>"
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += "." + p
	}
	return result
}

func bindingPaths(bindings []config.Binding) []string {
	paths := make([]string, len(bindings))
	for i, b := range bindings {
		paths[i] = formatBindingPath(b)
	}
	return paths
}

// bindingDiagnostic constructs an E0601 diagnostic from a binding failure.
func bindingDiagnostic(rule config.Rule, bf *bindingFailure) diag.Diagnostic {
	pos := rule.When.Pos()
	if rule.WhenSyntax != nil {
		pos = rule.WhenSyntax.Pos()
	}
	for _, b := range rule.Bindings {
		if b.Variable == bf.variable && b.Pos.IsValid() {
			pos = b.Pos
			break
		}
	}
	return diag.Diagnostic{
		Code:     diag.E0601.Code,
		Severity: diag.SeverityError,
		Title:    "binding variable mismatch",
		Primary: diag.Label{
			Pos: pos,
			Len: len("@bind(" + bf.variable + ")"),
			Msg: "@bind(" + bf.variable + "): values differ",
			Reasons: []diag.Reason{
				diag.BindingMismatch{
					Variable: bf.variable,
					Paths:    bf.paths,
					Values:   bf.values,
				},
			},
		},
		Help: diag.E0601.Help,
	}
}
