package rules

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: command: =~"^git\\s+add\\s+.*(\\.env\\b|credentials\\.json\\b|id_rsa\\b)"
	}
	then: deny: {
		rule_id:  "secret-file"
		reason:   "Refusing to stage a likely secret file"
		severity: "HIGH"
	}
}
