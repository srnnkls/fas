package rules

_int: int

// Rule caps retry_count at 10; an input above that triggers a BoundViolation
// with a concrete distance ("off by N") on the caret row.
bound_violation: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: retry_count: _int & <=10
	}
	then: deny: {
		rule_id:  "bound-violation"
		reason:   "too many retries"
		severity: "HIGH"
	}
}
