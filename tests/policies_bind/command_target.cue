package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

// Deny when a move or copy command's source and destination are the same file.
// Exercises @bind path equality at the policy level: targets[0] must equal
// targets[1] for the rule to fire.
self_copy: {
	when: hook.#PreToolUse & tool.#Bash & {
		tool_input: parsed: targets: [...string] @bind(Path, 0) @bind(Path, 1)
	}
	then: deny: {
		rule_id:  "self-copy"
		reason:   "Source and destination are the same file"
		severity: "LOW"
	}
}
