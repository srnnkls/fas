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
