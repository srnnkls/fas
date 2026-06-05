// Package escalation defines the vocabulary of privilege-escalation commands
// and the structural matcher that flags tool invocations whose prefix commands
// include one of them.
package escalation

import "list"

// #EscalationCommands is the canonical list of privilege-escalation prefixes.
#EscalationCommands: ["sudo", "doas", "su"]

// #escalationCommand matches a single command string that is exactly one of
// #EscalationCommands.
#escalationCommand: or(#EscalationCommands)

// #hasPrivilegeEscalation asserts that
// `tool_input.parsed.attributes.prefix_commands` contains at least one entry
// matching #escalationCommand.
#hasPrivilegeEscalation: {
	tool_input: {parsed: {attributes: {prefix_commands: list.MatchN(>0, #escalationCommand), ...}, ...}, ...}
	...
}
