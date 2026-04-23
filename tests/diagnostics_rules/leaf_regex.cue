package rules

// Rule demands the command start with `rm `; feeding `ls` fails the
// leaf regex constraint and produces an E0301 diagnostic. The `when`
// block is a bare struct literal so the localize walker can descend
// into it (top-level unifications such as `hook.#PreToolUse & ...`
// produce a BinaryExpr, not a StructLit, and the walker skips those).
leaf_regex: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: command: =~"^rm "
	}
	then: deny: {
		rule_id:  "leaf-regex"
		reason:   "rm is blocked"
		severity: "HIGH"
	}
}
