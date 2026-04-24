package evaluator

import (
	"iter"

	"cuelang.org/go/cue"

	"github.com/srnnkls/quae/internal/config"
	"github.com/srnnkls/quae/internal/diag"
)

// LocalizeForTest exposes the unexported localize iterator to black-box
// tests so they can verify the range-over-func break contract directly,
// rather than inferring it through Evaluate's call site.
func LocalizeForTest(rule config.Rule, input cue.Value) iter.Seq[diag.Diagnostic] {
	return localize(rule, input)
}

// ProvenanceNotesForTest exposes the unexported provenanceNotes helper so
// T9's tests can assert cap/sort/dedup directly on constructed cue.Values
// without having to route through the full Evaluate pipeline.
func ProvenanceNotesForTest(ruleNext cue.Value, hostFile string) []diag.Label {
	return provenanceNotes(ruleNext, hostFile)
}
