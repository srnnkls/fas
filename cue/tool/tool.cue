// Package tool binds the tool identities in cue/catalog to the harness wire
// field `tool_name`. Rules match a specific tool by composing the event with a
// member — hook.#PreToolUse & tool.#Tool.Bash. Matchers over the *command* a
// Bash call runs (rm, tee, …) live in cue/command.
package tool

import "github.com/srnnkls/fas/cue/catalog"

// #Tool is the matcher set for the built-in tools: each member pins tool_name
// to one catalog identity, so it composes with an event via `&` the same way
// the command and path matchers do. A typo'd member (tool.#Tool.Bsh) is an
// undefined field the loader rejects, not a silent non-match. The event shapes
// keep tool_name an open string, so custom MCP/skill tools still match via
// {tool_name: "your-tool"}.
#Tool: {
	for k, v in catalog.#ToolName {
		(k): {tool_name: v, ...}
	}
}

// #KnownTool matches any built-in tool — the disjunction of the #Tool members.
// Compose as hook.#PreToolUse & tool.#KnownTool.
#KnownTool: or([for _, m in #Tool {m}])
