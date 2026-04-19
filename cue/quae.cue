package quae

import (
	"list"
	"strings"
)

#SystemPrefixes:     ["/etc", "/sys", "/proc", "/boot", "/dev"]
#EscalationCommands: ["sudo", "doas", "su"]
#DestructiveActions: ["delete", "drop", "remove", "destroy", "truncate"]

#systemTarget:      =~"^(\(strings.Join(#SystemPrefixes, "|")))"
#escalationCommand: or(#EscalationCommands)
#destructiveAction: or(#DestructiveActions)

#hasSystemTarget: {
	tool_input: parsed: targets: list.MatchN(>0, #systemTarget)
	...
}

#hasPrivilegeEscalation: {
	tool_input: parsed: attributes: prefix_commands: list.MatchN(>0, #escalationCommand)
	...
}

#hasDestructiveAction: {
	tool_input: parsed: actions: list.MatchN(>0, #destructiveAction)
	...
}

#isPreToolUse: {
	hook_event_name: "PreToolUse"
	...
}

#isUserPrompt: {
	hook_event_name: "UserPromptSubmit"
	...
}

#isBash: {
	tool_name: "Bash"
	...
}

#HasFlagMatching: {
	#re: string
	tool_input: parsed: flags: list.MatchN(>0, =~#re)
	...
}
