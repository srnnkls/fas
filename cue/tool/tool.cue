// Package tool defines per-tool matchers for the `tool_name` dispatched by
// the harness. New tool matchers (Write, Edit, etc.) get added here as the
// policy surface grows.
package tool

// #isBash: the invocation targets the Bash tool.
#isBash: {
	tool_name: "Bash"
	...
}
