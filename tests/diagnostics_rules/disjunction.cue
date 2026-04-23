package rules

// Rule accepts only three tool names; feeding any other value fails every
// arm and produces an E0401 diagnostic with one label per arm. The
// `when` block is a plain struct literal so the localize walker descends
// into it.
disjunction: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Read" | "Write" | "Edit"
	}
	then: deny: {
		rule_id:  "disjunction"
		reason:   "wrong tool"
		severity: "HIGH"
	}
}
