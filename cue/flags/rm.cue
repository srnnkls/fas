package quae

#rmShortClass: "friv"

#HasRmForce: {
	tool_input: parsed: flags: list.MatchN(>0, =~"^--force(=|$)|^-force(=|$)|^-[\(#rmShortClass)]*f[\(#rmShortClass)]*$")
	...
}

#HasRmRecursive: {
	tool_input: parsed: flags: list.MatchN(>0, =~"^--recursive(=|$)|^-recursive(=|$)|^-[\(#rmShortClass)]*r[\(#rmShortClass)]*$")
	...
}

#HasRmInteractive: {
	tool_input: parsed: flags: list.MatchN(>0, =~"^--interactive(=|$)|^-interactive(=|$)|^-[\(#rmShortClass)]*i[\(#rmShortClass)]*$")
	...
}

#HasRmVerbose: {
	tool_input: parsed: flags: list.MatchN(>0, =~"^--verbose(=|$)|^-verbose(=|$)|^-[\(#rmShortClass)]*v[\(#rmShortClass)]*$")
	...
}
