package rules

// Rule asks for a `signals.user_confirmed` path that the test input does
// not supply. The absent key under `signals` produces an E0201 diagnostic
// when --explain is on. The `when` block is a plain struct literal so the
// localize walker descends into it.
absent_path: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		signals: user_confirmed: true
	}
	then: deny: {
		rule_id:  "absent-path"
		reason:   "user must confirm"
		severity: "HIGH"
	}
}
