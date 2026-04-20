package flag

import "list"

// #rmShortClass is the set of short-letter flags recognized by `rm` (force,
// recursive, interactive, verbose). Using a concrete string constant — not a
// comprehension over an array — keeps the regex construction deterministic
// at parse time.
#rmShortClass: "friv"

// #HasRmForce matches long (--force, -force) and short-combo forms where 'f'
// appears anywhere inside the short-letter class.
#HasRmForce: {
	tool_input: parsed: flags: list.MatchN(>0, =~"^--force(=|$)|^-force(=|$)|^-[\(#rmShortClass)]*f[\(#rmShortClass)]*$")
	...
}

// #HasRmRecursive matches long (--recursive, -recursive) and short-combo
// forms where 'r' appears anywhere inside the short-letter class.
#HasRmRecursive: {
	tool_input: parsed: flags: list.MatchN(>0, =~"^--recursive(=|$)|^-recursive(=|$)|^-[\(#rmShortClass)]*r[\(#rmShortClass)]*$")
	...
}

// #HasRmInteractive matches long (--interactive, -interactive) and
// short-combo forms where 'i' appears anywhere inside the short-letter class.
#HasRmInteractive: {
	tool_input: parsed: flags: list.MatchN(>0, =~"^--interactive(=|$)|^-interactive(=|$)|^-[\(#rmShortClass)]*i[\(#rmShortClass)]*$")
	...
}

// #HasRmVerbose matches long (--verbose, -verbose) and short-combo forms
// where 'v' appears anywhere inside the short-letter class.
#HasRmVerbose: {
	tool_input: parsed: flags: list.MatchN(>0, =~"^--verbose(=|$)|^-verbose(=|$)|^-[\(#rmShortClass)]*v[\(#rmShortClass)]*$")
	...
}
