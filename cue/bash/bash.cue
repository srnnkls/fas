// Package bash matches the executable (and subcommand) invoked inside a Bash
// command. Prefer the parsed-fact matchers #command and #subcommand: they read
// parsed.commands/subcommands, so `sudo rm`, `FOO=1 rm`, and leading whitespace
// all match. Compose with tool.#Bash:
// hook.#PreToolUse & tool.#Bash & (bash.#command & {#name: "rm"}).
package bash

import "list"

// #command matches on parsed.commands (the resolved executable names) rather
// than the raw string, so it survives sudo/env/whitespace prefixes that defeat
// the ^cmd\b matchers below.
#command: {
	#name: string
	tool_input: {parsed: {commands: list.MatchN(>0, #name), ...}, ...}
	...
}

// #subcommand requires both the tool (#of in parsed.commands) and the
// subcommand (#name in parsed.subcommands), reading parsed facts so value-
// leaking global flags (`git -C /repo add`) cannot shadow the subcommand.
#subcommand: {
	#of:   string
	#name: string
	tool_input: {parsed: {
		commands:    list.MatchN(>0, #of)
		subcommands: list.MatchN(>0, #name)
		...
	}, ...}
	...
}

// #commandOrRaw matches #name in parsed.commands, OR — when the parser failed
// (attributes.parse_error present) — falls back to an anchored scan of the raw
// command string, so deny coverage survives malformed-but-executable input.
// #name must be a single literal command name (the fallback derives ^<name>\b).
#commandOrRaw: {
	#name: string
	tool_input: {parsed: {commands: list.MatchN(>0, #name), ...}, ...}
	...
} | {
	#name: string
	tool_input: {
		command: =~"^\(#name)\\b"
		parsed: {attributes: {parse_error: string, ...}, ...}
		...
	}
	...
}
