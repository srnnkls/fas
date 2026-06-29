package rules

// Uses `len()` inside `when` to compute over an input-derived field.
// The pattern materialises `flags` as `[]`, so `len(flags)` is always 0.
// Triggers E0508 at load time.
len_rule: {
	when: {
		tool_input: parsed: {
			flags: [...string]
			_n: len(flags)
			_n: >=2
		}
	}
	then: deny: {
		rule_id: "len-in-when"
		reason:  "too many flags"
	}
}
