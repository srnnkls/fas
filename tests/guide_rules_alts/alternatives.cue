package rules

import (
	"list"

	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

sibling_alt: {
	when: hook.#PreToolUse & tool.#Bash & {
		tool_input: {command: =~"^rm\\b", parsed: targets: [...=~"^/etc/"]}
	}
	then: deny: {rule_id: "sibling-alt", reason: "sibling-alt", severity: "HIGH"}
}

conjunction_alt: {
	when: hook.#PreToolUse & tool.#Bash & {
		tool_input: {parsed: flags: list.MatchN(>0, "--force"), command: =~"^git push"}
	}
	then: deny: {rule_id: "conjunction-alt", reason: "conjunction-alt", severity: "HIGH"}
}

count_alt: {
	when: hook.#PreToolUse & tool.#Bash & {
		tool_input: parsed: flags: list.MatchN(>=2, =~"^-")
	}
	then: deny: {rule_id: "count-alt", reason: "count-alt", severity: "LOW"}
}

close_alt: {
	when: hook.#PreToolUse & {tool_name: !="Bash"}
	then: deny: {rule_id: "close-alt", reason: "close-alt", severity: "LOW"}
}
