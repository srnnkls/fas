package flag

import "list"

// #hasRmForce matches long (--force, -force) and short-combo forms where 'f'
// appears anywhere inside a short-letter bundle.
#hasRmForce: {
	tool_input: {parsed: {flags: list.MatchN(>0, =~"^--force(=|$)|^-force(=|$)|^-[a-zA-Z]*f[a-zA-Z]*$"), ...}, ...}
	...
}

// #hasRmRecursive matches long (--recursive, -recursive) and short-combo
// forms where 'r' or 'R' appears anywhere inside a short-letter bundle.
#hasRmRecursive: {
	tool_input: {parsed: {flags: list.MatchN(>0, =~"^--recursive(=|$)|^-recursive(=|$)|^-[a-zA-Z]*[rR][a-zA-Z]*$"), ...}, ...}
	...
}

// #hasRmInteractive matches long (--interactive, -interactive) and
// short-combo forms where 'i' or 'I' appears anywhere inside a short-letter
// bundle.
#hasRmInteractive: {
	tool_input: {parsed: {flags: list.MatchN(>0, =~"^--interactive(=|$)|^-interactive(=|$)|^-[a-zA-Z]*[iI][a-zA-Z]*$"), ...}, ...}
	...
}

// #hasRmVerbose matches long (--verbose, -verbose) and short-combo forms
// where 'v' appears anywhere inside a short-letter bundle.
#hasRmVerbose: {
	tool_input: {parsed: {flags: list.MatchN(>0, =~"^--verbose(=|$)|^-verbose(=|$)|^-[a-zA-Z]*v[a-zA-Z]*$"), ...}, ...}
	...
}
