package rules

import "list"

// Uses `if` comprehension inside `when` to conditionally add constraints.
// The guard evaluates against the pattern's type, not the input's value.
// Triggers E0507 at load time.
if_rule: {
	when: {
		tool_input: parsed: {
			flags: [...string]
			if list.Contains(flags, "--force") {
				commands: [...=~"^git$"]
			}
		}
	}
	then: deny: {
		rule_id: "if-in-when"
		reason:  "forced git commands blocked"
	}
}
