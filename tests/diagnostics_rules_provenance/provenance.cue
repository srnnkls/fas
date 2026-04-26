package rules

import (
	"github.com/srnnkls/fas/cue/path"
)

// Rule constrains tool_input.command via a stdlib-defined regex
// (`path.#systemInCommand`). When the input fails the constraint, the
// localize walker harvests cross-file conjuncts on `ruleNext` and emits
// Provenance Notes pointing at the stdlib file. The text renderer surfaces
// each Provenance Note as a `= note: constraint introduced at <f:l:c>`
// footer, bypassing the SourceCache.LineAt filter that other Notes pass
// through (the Span carries the coordinates directly; no token.Pos exists
// for these synthetic Notes).
provenance: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: command: path.#systemInCommand
	}
	then: deny: {
		rule_id:  "provenance"
		reason:   "system path"
		severity: "HIGH"
	}
}
