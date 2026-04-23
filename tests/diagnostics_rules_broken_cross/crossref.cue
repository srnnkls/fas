package rules

// Two rules; the second reaches into the first's `when` subtree via
// selector expression. Triggers E0502 at load time.
base_rule: {
	when: {tool_name: "Bash"}
	then: deny: {
		rule_id: "base"
		reason:  "nope"
	}
}

consumer_rule: {
	when: {tool_name: base_rule.when.tool_name}
	then: deny: {
		rule_id: "consumer"
		reason:  "nope"
	}
}
