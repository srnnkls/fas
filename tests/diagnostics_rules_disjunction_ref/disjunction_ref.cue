package rules

// Variant of the disjunction rule where the disjunction lives in a
// hidden-sibling definition rather than as a literal on the field. The
// localize walker should still emit E0401 with ranked arms, surfacing
// "closest arm was X" + a "= note: other ranked arms:" footer — same
// shape as the literal-on-field case (`disjunction_close`). Input "Rea"
// is Levenshtein 1 from "Read"; the top arm scores above
// ScoreKindMatch so the renderer takes the close-arm path.
_#ToolKind: "Read" | "Write" | "Edit"

disjunction_ref: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       _#ToolKind
	}
	then: deny: {
		rule_id:  "disjunction-ref"
		reason:   "wrong tool"
		severity: "HIGH"
	}
}
