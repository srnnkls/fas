package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
	"github.com/srnnkls/fas/cue/bash"
)

audit_bash: {
	when: hook.#PreToolUse & tool.#Bash
	then: inject: {
		rule_id: "audit-bash"
		channel: "agent"
		text:    "Bash call audited by the global policy layer."
	}
}

block_force_add: {
	when: hook.#PreToolUse & tool.#Bash &
		(bash.#subcommand & {#of: "git", #name: "add"}) & {
		tool_input: parsed: flags: [..."--force"]
	}
	then: deny: {
		rule_id:  "global-no-force-add"
		reason:   "git add --force is blocked by the global policy layer"
		severity: "HIGH"
	}
}
