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

#isRm: {tool_input: command: =~"^rm\\b", ...}
#isChmod: {tool_input: command: =~"^chmod\\b", ...}
#isChown: {tool_input: command: =~"^chown\\b", ...}
#isChgrp: {tool_input: command: =~"^chgrp\\b", ...}
#isDd: {tool_input: command: =~"^dd\\b", ...}
#isTruncate: {tool_input: command: =~"^truncate\\b", ...}
#isTee: {tool_input: command: =~"^tee\\b", ...}
#isInstall: {tool_input: command: =~"^install\\b", ...}
#isCp: {tool_input: command: =~"^cp\\b", ...}
#isMv: {tool_input: command: =~"^mv\\b", ...}
#isLn: {tool_input: command: =~"^ln\\b", ...}
#isMkdir: {tool_input: command: =~"^mkdir\\b", ...}
#isRmdir: {tool_input: command: =~"^rmdir\\b", ...}
#isTouch: {tool_input: command: =~"^touch\\b", ...}
