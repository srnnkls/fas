package rules

import (
	"github.com/srnnkls/quae/cue/hook"
	"github.com/srnnkls/quae/cue/tool"
)

// Secondary match: the parser's AST walker extracts CallExpr args, so tokens
// that only appear in control-flow clause heads — e.g. `for f in /etc/*.conf`
// — never reach parsed.targets. A raw-command regex closes that gap without
// changing the parser: the ForClause header path still surfaces here.
//
// The pattern anchors the system prefix at a word boundary so it cannot match
// substrings inside unrelated identifiers. `./build`, `./node_modules`, and
// `src/main.py` never contain `/etc|/sys|/proc|/boot|/dev` after a boundary.
rule: {
	when: hook.#PreToolUse & tool.#isBash & {
		tool_input: command: =~"(^|[^A-Za-z0-9_])/(etc|sys|proc|boot|dev)(/|$|[^A-Za-z0-9_])"
	}
	then: deny: {
		rule_id:  "system-path-command"
		reason:   "System path blocked"
		severity: "HIGH"
	}
}
