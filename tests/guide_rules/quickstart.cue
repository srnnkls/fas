package rules

import (
	"list"

	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

no_rm_home: {
	when: hook.#PreToolUse & tool.#Bash & {
		tool_input: {
			command: =~"^rm\\b"
			parsed: {
				flags:   list.MatchN(>0, =~"^-[a-zA-Z]*r[a-zA-Z]*$|^--recursive$")
				targets: list.MatchN(>0, =~"^(~|\\$HOME)$")
			}
		}
	}
	then: deny: {
		rule_id:  "no-rm-home"
		reason:   "Recursive deletion of the home directory is blocked"
		severity: "HIGH"
	}
}
