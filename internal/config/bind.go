package config

import (
	"fmt"
	"strconv"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

// Binding records a single @bind(Variable) or @bind(Variable, SubPath)
// attribute found on a field inside a rule's `when` clause. Two fields
// annotated with the same Variable name declare a path-equality constraint:
// the concrete input values at those paths must unify to the same point in
// the lattice.
//
// SubPath is an optional element accessor applied after resolving FieldPath
// against the input — typically a list index ("0", "1", …). An empty
// SubPath means the field's own value is bound directly.
type Binding struct {
	Variable  string
	FieldPath []string
	SubPath   string
	Pos       token.Pos
}

// SubIndex returns the integer list index when SubPath is a non-negative
// integer, and -1 otherwise.
func (b Binding) SubIndex() int {
	if b.SubPath == "" {
		return -1
	}
	n, err := strconv.Atoi(b.SubPath)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// extractBindings walks a `when` AST and returns every @bind attribute it
// finds, with the accumulated struct path and optional sub-path argument.
// Returns nil when no bindings are present (the common case).
func extractBindings(whenExpr ast.Expr) ([]Binding, error) {
	var bindings []Binding
	if err := walkBindings(whenExpr, nil, &bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

func walkBindings(expr ast.Expr, path []string, out *[]Binding) error {
	st, ok := expr.(*ast.StructLit)
	if !ok {
		return nil
	}
	for _, decl := range st.Elts {
		f, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		name, _, err := ast.LabelName(f.Label)
		if err != nil || name == "" {
			continue
		}
		fieldPath := append(append([]string(nil), path...), name)

		for _, attr := range f.Attrs {
			variable, subPath, err := parseBindAttr(attr.Text)
			if err != nil {
				return fmt.Errorf("field %s: %w", strings.Join(fieldPath, "."), err)
			}
			if variable == "" {
				continue
			}
			*out = append(*out, Binding{
				Variable:  variable,
				FieldPath: fieldPath,
				SubPath:   subPath,
				Pos:       attr.Pos(),
			})
		}

		if inner, ok := f.Value.(*ast.StructLit); ok {
			if err := walkBindings(inner, fieldPath, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseBindAttr parses an attribute text like "@bind(X)" or "@bind(X,0)".
// Returns ("", "", nil) for non-@bind attributes.
func parseBindAttr(text string) (variable, subPath string, err error) {
	body, ok := strings.CutPrefix(text, "@bind(")
	if !ok {
		return "", "", nil
	}
	body, ok = strings.CutSuffix(body, ")")
	if !ok {
		return "", "", fmt.Errorf("malformed @bind attribute: %s", text)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", fmt.Errorf("@bind requires a variable name: %s", text)
	}

	parts := strings.SplitN(body, ",", 2)
	variable = strings.TrimSpace(parts[0])
	if variable == "" {
		return "", "", fmt.Errorf("@bind variable name is empty: %s", text)
	}
	if !isBindVariable(variable) {
		return "", "", fmt.Errorf("@bind variable name %q must be an uppercase letter or identifier: %s", variable, text)
	}
	if len(parts) == 2 {
		subPath = strings.TrimSpace(parts[1])
		if subPath == "" {
			return "", "", fmt.Errorf("@bind sub-path is empty: %s", text)
		}
	}
	return variable, subPath, nil
}

// isBindVariable checks that a variable name is a valid identifier starting
// with an uppercase letter.
func isBindVariable(s string) bool {
	if s == "" {
		return false
	}
	first := true
	for _, r := range s {
		if first {
			if r < 'A' || r > 'Z' {
				return false
			}
			first = false
			continue
		}
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}
