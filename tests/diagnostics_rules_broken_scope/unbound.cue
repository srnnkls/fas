package rules

// References bare identifier `myUnknownVar` — not declared locally and not
// exported by the stdlib. Triggers E0501 at load time.
uid_rule: {
	when: {tool_name: myUnknownVar}
	then: deny: {
		rule_id: "uid"
		reason:  "nope"
	}
}
