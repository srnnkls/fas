// Package flag exposes the generic building block for flag-based constraints
// plus per-tool flag helpers (see rm.cue, future: write.cue, edit.cue, etc.).
package flag

import "list"

// #hasFlagMatching is the generic building block: unify with `{#re: "^--x$"}`
// to produce a constraint that matches any `tool_input.parsed.flags` list
// containing at least one entry matching the regex.
#hasFlagMatching: {
	#re: string
	tool_input: {parsed: {flags: list.MatchN(>0, =~#re), ...}, ...}
	...
}

// #hasOption is regex-free set membership over parsed.flags. The
// `or([for s in #spellings {s}])` idiom turns the data list into a disjunction
// constraint — a bare list literal would not constrain (works for one element).
#hasOption: {
	#spellings: [string, ...string]
	tool_input: {parsed: {flags: list.MatchN(>0, or([for s in #spellings {s}])), ...}, ...}
	...
}
