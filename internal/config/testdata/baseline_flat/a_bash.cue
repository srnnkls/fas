package rules

deny_bash: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {
		rule_id: "a-bash"
		reason:  "bash blocked"
	}
}
