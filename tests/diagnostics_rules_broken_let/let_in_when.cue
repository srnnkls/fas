package rules

// Uses `let` inside `when` to name an input path. The binding captures
// the pattern's type, not the input's value. Triggers E0506 at load time.
let_rule: {
	when: {
		let cmd = tool_input.command
		tool_input: command: string
		_check: cmd & =~"^git"
	}
	then: deny: {
		rule_id: "let-in-when"
		reason:  "git commands blocked"
	}
}
