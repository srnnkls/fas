package rules

// Rule demands `tool_input.command`; the payload supplies the mistyped
// `commnd` (Levenshtein 1). The renderer surfaces a `= hint: did you mean`
// footer under the key-missing help.
key_missing_hint: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: command: "ls"
	}
	then: deny: {
		rule_id:  "key-missing-hint"
		reason:   "command typo"
		severity: "HIGH"
	}
}
