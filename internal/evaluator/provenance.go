package evaluator

import (
	"sort"

	"cuelang.org/go/cue"

	"github.com/srnnkls/fas/internal/diag"
)

// maxProvenanceEntries caps Provenance footer notes per diagnostic. The
// footer is a debugging aid; beyond a handful of entries it overwhelms the
// rendered diagnostic. See scope.md F6 / AD-4.
const maxProvenanceEntries = 3

// provenanceNotes walks ruleNext.Expr() recursively and returns footer labels
// for each conjunct whose Pos().Filename() differs from hostFile. Entries are
// deduped by (file, line), sorted by (file, line), and capped at
// maxProvenanceEntries. Conjuncts with unresolvable positions (empty filename
// or invalid Pos) are skipped silently so NF3 (no panic on missing positions)
// holds. Snippet is left empty for now — the renderer falls back on the
// file:line:col location string alone.
//
// Before walking, the value is passed through cue.Value.Eval() so references
// (selectors like `path.#systemInCommand`) are resolved to their defining
// conjuncts. Without Eval() the walker would only see the selector itself at
// the host file, losing the stdlib origin that motivates this footer.
func provenanceNotes(ruleNext cue.Value, hostFile string) []diag.Label {
	type key struct {
		file string
		line int
	}
	seen := make(map[key]diag.Span)
	var collect func(v cue.Value)
	collect = func(v cue.Value) {
		op, ops := v.Expr()
		if op == cue.AndOp || op == cue.OrOp {
			for _, o := range ops {
				collect(o)
			}
			return
		}
		pos := v.Pos()
		if !pos.IsValid() {
			return
		}
		file := pos.Filename()
		if file == "" || file == hostFile {
			return
		}
		k := key{file: file, line: pos.Line()}
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = diag.Span{
			File: file,
			Line: pos.Line(),
			Col:  pos.Column(),
		}
	}
	collect(ruleNext.Eval())

	if len(seen) == 0 {
		return nil
	}
	spans := make([]diag.Span, 0, len(seen))
	for _, sp := range seen {
		spans = append(spans, sp)
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].File != spans[j].File {
			return spans[i].File < spans[j].File
		}
		return spans[i].Line < spans[j].Line
	})
	if len(spans) > maxProvenanceEntries {
		spans = spans[:maxProvenanceEntries]
	}
	labels := make([]diag.Label, 0, len(spans))
	for _, sp := range spans {
		labels = append(labels, diag.Label{
			Reasons: []diag.Reason{diag.Provenance{Span: sp}},
		})
	}
	return labels
}
