// Package path defines the vocabulary of OS-level system prefixes and the
// structural matcher that flags tool invocations whose `parsed.targets`
// includes one of them.
package path

import (
	"list"
	"strings"
)

// #SystemPrefixes is the canonical list of read-only system directories
// quae treats as "never-touch" targets.
#SystemPrefixes: ["/etc", "/sys", "/proc", "/boot", "/dev"]

// #systemTarget matches a single path string that begins with one of
// #SystemPrefixes. The anchor is deliberately strict: `./etc/foo` must NOT
// match (sdl-mcp false-positive guard).
#systemTarget: =~"^(\(strings.Join(#SystemPrefixes, "|")))"

// #hasSystemTarget asserts that `tool_input.parsed.targets` contains at
// least one entry matching #systemTarget.
#hasSystemTarget: {
	tool_input: parsed: targets: list.MatchN(>0, #systemTarget)
	...
}
