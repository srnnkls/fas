package rules

// Hidden sibling to bypass the bare-kind lint (loader rejects naked `int`
// in `when` unless it resolves to a hidden sibling or stdlib import).
_int: int

// Rule demands tool_input.command be an int; a string command fails the
// kind constraint and produces an E0303 with a KindMismatch Reason.
kind_mismatch: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: command: _int
	}
	then: deny: {
		rule_id:  "kind-mismatch"
		reason:   "command must be int"
		severity: "HIGH"
	}
}
