package rules

charlie: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "m-charlie"
		reason:  "c"
	}
}

alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "m-alpha"
		reason:  "a"
	}
}
