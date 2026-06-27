package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

remind_on_webfetch: {
	when: hook.#PreToolUse & tool.#WebFetch
	then: inject: {
		rule_id:  "webfetch-reminder"
		channel:  "agent"
		priority: 60
		text:     "Prefer the local docs cache before fetching the network."
	}
}

confirm_force_push: {
	when: hook.#PreToolUse & tool.#Bash & {
		tool_input: command: =~"git push.*--force"
	}
	then: ask: {
		rule_id:  "confirm-force-push"
		reason:   "Force-push rewrites remote history."
		question: "Force-push this branch?"
	}
}
