// Package command matches the executable (and subcommand) invoked inside a Bash
// command. Prefer the parsed-fact matchers #command and #subcommand: they read
// parsed.commands/subcommands, so `sudo rm`, `FOO=1 rm`, and leading whitespace
// all match. Compose with tool.#Tool.Bash:
// hook.#PreToolUse & tool.#Tool.Bash & (command.#command & {#name: "rm"}).
//
// The legacy #isRm/#isChmod/#isTee/#isMv defs anchor on the raw command string
// (^cmd\b) and therefore do NOT survive sudo/env/whitespace prefixes. They are
// deprecated pending removal (T6); use #command instead.
package command

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

// #commandRobust matches #name in parsed.commands, OR — when the parser failed
// (attributes.parse_error present) — falls back to an anchored scan of the raw
// command string, so deny coverage survives malformed-but-executable input.
// #name must be a single literal command name (the fallback derives ^<name>\b).
#commandRobust: {
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

#isRm: {tool_input: {command: =~"^rm\\b", ...}, ...}
#isChmod: {tool_input: {command: =~"^chmod\\b", ...}, ...}
#isTee: {tool_input: {command: =~"^tee\\b", ...}, ...}
#isMv: {tool_input: {command: =~"^mv\\b", ...}, ...}
