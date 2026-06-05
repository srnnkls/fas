// Package path defines the vocabulary of OS-level system prefixes and the
// structural matcher that flags tool invocations whose `parsed.targets`
// includes one of them.
package path

import (
	"list"
	"strings"
)

// #SystemPrefixes is the canonical list of read-only system directories
// fas treats as "never-touch" targets.
#SystemPrefixes: ["/etc", "/sys", "/proc", "/boot", "/dev"]

// #systemTarget matches a single path string that begins with one of
// #SystemPrefixes. The anchor is deliberately strict: `./etc/foo` must NOT
// match (sdl-mcp false-positive guard).
#systemTarget: =~"^(\(strings.Join(#SystemPrefixes, "|")))"

// #hasSystemTarget asserts that `tool_input.parsed.targets` contains at
// least one entry matching #systemTarget.
#hasSystemTarget: {
	tool_input: {parsed: {targets: list.MatchN(>0, #systemTarget), ...}, ...}
	...
}

// #InCommandRe is a parameterized regex builder. Unify with a `#prefixes`
// list (and optionally `#extra` for caller-supplied additions) and read `.out`
// to get a constraint matching any of those paths embedded in a command
// string.
#InCommandRe: {
	#prefixes: [...string]
	#extra: [...string] | *[]
	_all: list.Concat([#prefixes, #extra])
	_dirs: strings.Join([for p in _all {strings.TrimPrefix(p, "/")}], "|")
	out:   =~"(^|[^A-Za-z0-9_])/(\(_dirs))(/|$|[^A-Za-z0-9_])"
}

// #systemInCommand matches a system path embedded anywhere in a command
// string.  The boundary guards ensure `foo/etc` does not match — only paths
// where the leading `/` is preceded by a non-word character or start of string.
#systemInCommand: (#InCommandRe & {#prefixes: #SystemPrefixes}).out

// #hasSystemInCommand asserts that `tool_input.command` contains a reference
// to one of the system directories, e.g. `cat /etc/passwd` or `ls /proc`.
#hasSystemInCommand: {
	tool_input: {command: #systemInCommand, ...}
	...
}
