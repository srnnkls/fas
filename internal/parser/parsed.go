package parser

// Parsed is the canonical shape parsers write to tool_input.parsed.
//
// Parsers extract:
//   - Actions: semantic verbs (e.g. "remove", "delete"). Never command names.
//   - Commands: resolved command names, one per call (post escalation strip).
//     A flat union of every CallExpr name in the AST (including control-flow
//     condition commands), safe for deny-direction matching.
//   - Subcommands: registered subcommands resolved per call (e.g. "commit").
//   - Targets: positional args that look like paths or refs.
//   - Flags: debundled flag tokens.
//   - Attributes: tool-specific parsed details (escalation, pipelines, redirections, etc).
type Parsed struct {
	Actions     []string       `json:"actions"`
	Commands    []string       `json:"commands"`
	Subcommands []string       `json:"subcommands"`
	Targets     []string       `json:"targets"`
	Flags       []string       `json:"flags"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}
