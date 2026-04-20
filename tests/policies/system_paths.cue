package rules

import (
	"github.com/srnnkls/quae/cue/hook"
	"github.com/srnnkls/quae/cue/tool"
	"github.com/srnnkls/quae/cue/path"
)

// Primary match: parsed.targets references a system prefix. Covers simple
// commands and compound forms whose AST walker surfaces the path in targets
// (e.g. `echo start && rm -rf /etc/passwd` — both commands' args are walked).
rule: {
	when: hook.#PreToolUse & tool.#isBash & path.#hasSystemTarget
	then: deny: {
		rule_id:  "system-path"
		reason:   "System path blocked"
		severity: "HIGH"
	}
}
