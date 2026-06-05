package rules

import (
	"list"

	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
	"github.com/srnnkls/fas/cue/command"
	"github.com/srnnkls/fas/cue/flag"
)

destructive_home: {
	when: {
		hook.#PreToolUse
		tool.#Tool.Bash
		command.#isRm
		flag.#HasRmRecursive
		tool_input: parsed: targets: list.MatchN(>0, =~"^(~|\\$HOME)$")
	}
	then: deny: {
		rule_id:  "destructive-home"
		reason:   "Recursive deletion of the home directory is blocked"
		severity: "HIGH"
	}
}
