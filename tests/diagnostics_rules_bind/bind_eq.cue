package rules

// Source and destination bound to the same variable: the first and second
// targets must be equal. Catches self-referencing mv/cp/ln operations.
// When they differ the evaluator emits E0601 under --explain.
bind_eq: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: [...string] @bind(Path, 0) @bind(Path, 1)
	}
	then: deny: {
		rule_id:  "bind-eq"
		reason:   "source and destination are the same"
		severity: "MEDIUM"
	}
}
