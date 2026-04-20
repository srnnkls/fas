package rules

import (
	"list"

	"github.com/srnnkls/quae/cue:quae"
)

rule: {
	when: quae.#PreToolUse & quae.#isBash & {
		tool_input: {
			command: =~"^rm\\b"
			parsed: {
				flags:   list.MatchN(>0, =~"^-[a-zA-Z]*r[a-zA-Z]*$|^--recursive$")
				targets: list.MatchN(>0, =~"^(~|\\$HOME)$")
			}
		}
	}
	then: deny: {
		rule_id:  "destructive-home"
		reason:   "Recursive deletion of the home directory is blocked"
		severity: "HIGH"
	}
}
