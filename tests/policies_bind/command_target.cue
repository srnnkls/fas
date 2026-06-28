package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

// Deny when the first parsed target exactly equals the parsed command name.
// Exercises @bind path equality at the policy level.
command_is_target: {
	when: hook.#PreToolUse & tool.#Bash & {
		tool_input: parsed: {
			commands: [...string] @bind(X, 0)
			targets:  [...string] @bind(X, 0)
		}
	}
	then: deny: {
		rule_id:  "command-is-target"
		reason:   "Command equals its own first target"
		severity: "LOW"
	}
}
