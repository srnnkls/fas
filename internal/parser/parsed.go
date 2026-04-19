package parser

// Parsed is the canonical shape parsers write to tool_input.parsed.
//
// Parsers extract:
//   - Actions: semantic verbs (e.g. "remove", "delete"). Never command names.
//   - Targets: positional args that look like paths or refs.
//   - Flags: raw flag tokens, not normalized.
//   - Attributes: tool-specific parsed details (escalation, pipelines, redirections, etc).
type Parsed struct {
	Actions    []string       `json:"actions"`
	Targets    []string       `json:"targets"`
	Flags      []string       `json:"flags"`
	Attributes map[string]any `json:"attributes,omitempty"`
}
