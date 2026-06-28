package rules

// Two fields bound to the same variable: the first parsed command must equal
// the first parsed target. When they differ the evaluator emits E0601 under
// --explain.
bind_eq: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: {
			commands: [...string] @bind(X, 0)
			targets:  [...string] @bind(X, 0)
		}
	}
	then: deny: {
		rule_id:  "bind-eq"
		reason:   "command name equals first target"
		severity: "MEDIUM"
	}
}
