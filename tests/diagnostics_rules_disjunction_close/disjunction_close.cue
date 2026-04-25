package rules

// Variant of the disjunction rule tuned so the input "Rea" lands within
// Levenshtein range of "Read" (distance 1). rankArms lifts the top arm's
// score above ScoreKindMatch so the renderer emits the ranked "closest arm
// was X" primary plus a `= note: other ranked arms:` footer listing
// runner-up arms in rank order.
disjunction_close: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Read" | "Write" | "Edit"
	}
	then: deny: {
		rule_id:  "disjunction-close"
		reason:   "wrong tool"
		severity: "HIGH"
	}
}
