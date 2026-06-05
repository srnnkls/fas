// Package action defines the semantic-verb vocabulary of destructive actions
// and the structural matcher that flags tool invocations whose parsed actions
// include one of them. Command names (e.g. `rm`, `psql`) deliberately stay
// out of this list — they are command tokens, not semantic verbs.
package action

import "list"

// #DestructiveActions is the canonical semantic-verb vocabulary.
#DestructiveActions: ["delete", "drop", "remove", "destroy", "truncate"]

// #destructiveAction matches a single string that is exactly one of
// #DestructiveActions.
#destructiveAction: or(#DestructiveActions)

// #hasDestructiveAction asserts that `tool_input.parsed.actions` contains at
// least one entry matching #destructiveAction.
#hasDestructiveAction: {
	tool_input: {parsed: {actions: list.MatchN(>0, #destructiveAction), ...}, ...}
	...
}
