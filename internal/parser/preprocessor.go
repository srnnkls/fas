package parser

import "maps"

// Preprocess dispatches to a builtin parser by tool name and returns an input
// with tool_input.parsed populated. Unknown tools pass through unchanged.
//
// Parsers write only to the tool_input.parsed namespace. No other fields are
// mutated. Calling Preprocess twice on the same input yields an equal result.
//
// Known tools with malformed tool_input (missing or non-map) surface a
// Parsed{} with Attributes.parse_error rather than silently passing through.
func Preprocess(toolName string, input map[string]any) (map[string]any, error) {
	parse, known := builtinParsers[toolName]
	if !known {
		return input, nil
	}

	toolInput, ok := input["tool_input"].(map[string]any)
	if !ok {
		out := maps.Clone(input)
		out["tool_input"] = map[string]any{
			"parsed": Parsed{
				Attributes: map[string]any{
					"parse_error": "tool_input missing or not an object",
				},
			},
		}
		return out, nil
	}
	if _, already := toolInput["parsed"]; already {
		return input, nil
	}

	command, _ := toolInput["command"].(string)
	parsed := parse(command)

	out := maps.Clone(input)
	nextToolInput := maps.Clone(toolInput)
	nextToolInput["parsed"] = parsed
	out["tool_input"] = nextToolInput
	return out, nil
}

// builtinParsers pairs a tool name with the bash-shaped command parser it
// should run. Parsers all consume the command string from tool_input.command.
var builtinParsers = map[string]func(string) Parsed{
	"Bash": ParseBash,
}
