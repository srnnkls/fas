package config

import (
	"cuelang.org/go/cue/ast"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"

	"github.com/srnnkls/fas/internal/diag"
)

func stdlibSuggestion(file *ast.File, err error) *diag.Label {
	if file == nil {
		return nil
	}
	var cueErr cueerrors.Error
	if !cueerrors.As(err, &cueErr) {
		return nil
	}
	for _, leaf := range cueerrors.Errors(cueErr) {
		for _, pos := range errorPositions(leaf) {
			sel := findSelectorAt(file, pos)
			if sel == nil {
				continue
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				continue
			}
			missing, ok := sel.Sel.(*ast.Ident)
			if !ok {
				continue
			}
			msg := suggest(x.Name, missing.Name, nil, StdlibIndex())
			if msg == "" {
				continue
			}
			return &diag.Label{
				Pos: sel.Pos(),
				Len: len([]rune(x.Name)) + 1 + len([]rune(missing.Name)),
				Msg: msg,
			}
		}
	}
	return nil
}

func errorPositions(e cueerrors.Error) []token.Pos {
	out := e.InputPositions()
	if p := e.Position(); p.IsValid() {
		out = append(out, p)
	}
	return out
}

func findSelectorAt(file *ast.File, pos token.Pos) *ast.SelectorExpr {
	var found *ast.SelectorExpr
	ast.Walk(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		sp := sel.Sel.Pos()
		if sp.Line() == pos.Line() && sp.Column() == pos.Column() {
			found = sel
			return false
		}
		return true
	}, nil)
	return found
}
