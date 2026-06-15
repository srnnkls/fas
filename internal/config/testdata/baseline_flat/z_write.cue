package rules

deny_write: {
	when: {hook_event_name: "PreToolUse", tool_name: "Write"}
	then: deny: {
		rule_id: "z-write"
		reason:  "write blocked"
	}
}
