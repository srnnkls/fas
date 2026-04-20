// Package flag exposes the generic building block for flag-based constraints
// plus per-tool flag helpers (see rm.cue, future: write.cue, edit.cue, etc.).
package flag

import "list"

// #HasFlagMatching is the generic building block: unify with `{#re: "^--x$"}`
// to produce a constraint that matches any `tool_input.parsed.flags` list
// containing at least one entry matching the regex.
#HasFlagMatching: {
	#re: string
	tool_input: parsed: flags: list.MatchN(>0, =~#re)
	...
}
