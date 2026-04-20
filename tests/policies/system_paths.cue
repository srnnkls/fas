package rules

import (
	"github.com/srnnkls/quae/cue/hook"
	"github.com/srnnkls/quae/cue/tool"
	"github.com/srnnkls/quae/cue/path"
)

// Primary match: parsed.targets references a system prefix. Covers simple
// commands and compound forms whose AST walker surfaces the path in targets
// (e.g. `echo start && rm -rf /etc/passwd` — both commands' args are walked).
targets: {
	when: hook.#PreToolUse & tool.#isBash & path.#hasSystemTarget
	then: deny: {
		rule_id:  "system-path"
		reason:   "System path blocked"
		severity: "HIGH"
	}
}

// Secondary match: the parser's AST walker extracts CallExpr args, so tokens
// that only appear in control-flow clause heads — e.g. `for f in /etc/*.conf`
// — never reach parsed.targets. A raw-command regex closes that gap without
// changing the parser: the ForClause header path still surfaces here.
//
// The pattern anchors the system prefix at a word boundary so it cannot match
// substrings inside unrelated identifiers. `./build`, `./node_modules`, and
// `src/main.py` never contain `/etc|/sys|/proc|/boot|/dev` after a boundary.
for_loop: {
	when: hook.#PreToolUse & tool.#isBash & {
		tool_input: command: =~"(^|[^A-Za-z0-9_])/(etc|sys|proc|boot|dev)(/|$|[^A-Za-z0-9_])"
	}
	then: deny: {
		rule_id:  "system-path-command"
		reason:   "System path blocked"
		severity: "HIGH"
	}
}
