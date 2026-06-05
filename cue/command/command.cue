// Package command matches the executable invoked inside a Bash command via
// tool_input.command. Compose with tool.#Tool.Bash to constrain shell
// invocations: hook.#PreToolUse & tool.#Tool.Bash & command.#isRm.
//
// Matchers anchor on the raw command string (^cmd\b), so they see only the
// leading token: `sudo rm`, `FOO=1 rm`, and leading whitespace do NOT match.
// This raw-string prefix limitation is deliberate but asymmetric with the path
// matchers, which read parser output (parsed.targets) and survive such
// prefixes — so in a composed rule like `command.#isTee & path.#hasSystemInCommand`
// the path half is prefix-robust while the command half is not.
package command

#isRm: {tool_input: {command: =~"^rm\\b", ...}, ...}
#isChmod: {tool_input: {command: =~"^chmod\\b", ...}, ...}
#isTee: {tool_input: {command: =~"^tee\\b", ...}, ...}
#isMv: {tool_input: {command: =~"^mv\\b", ...}, ...}
