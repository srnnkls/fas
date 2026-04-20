package quae

// Typed hook events. Each definition pins hook_event_name to a single literal
// and layers the per-event required fields on top of #Input, so rule authors
// who write `when: quae.#PreToolUse & ...` get CUE to enforce that the input
// actually carries the fields the rule's downstream clauses assume. The
// looser #is* matchers in quae.cue remain available for rules that only care
// about the event name.

// #PreToolUse: a tool invocation is about to run — tool_name must be present.
#PreToolUse: #Input & {
	hook_event_name: "PreToolUse"
	tool_name:       string & !=""
}

// #PostToolUse: a tool invocation just finished — tool_name is required and
// tool_response rides along inside tool_input so signals can inspect the result.
// `parsed` mirrors #Input.tool_input.parsed as an open struct so rule authors
// can compose #PostToolUse with stdlib constraints that add list.MatchN
// assertions under `tool_input.parsed` without triggering CUE's eager
// empty-list evaluation.
#PostToolUse: #Input & {
	hook_event_name: "PostToolUse"
	tool_name:       string & !=""
	tool_input?: {
		command?:       string
		parsed?:        {...}
		tool_response?: _
		...
	}
}

// #UserPromptSubmit: the user submitted a prompt — prompt must be non-empty.
#UserPromptSubmit: #Input & {
	hook_event_name: "UserPromptSubmit"
	prompt:          string & !=""
}

// #Stop: the session is stopping — no extra fields beyond the event name.
#Stop: #Input & {
	hook_event_name: "Stop"
}

// #SubagentStart: a subagent is about to start — no extra fields required.
#SubagentStart: #Input & {
	hook_event_name: "SubagentStart"
}

// #Notification: a harness-level notification fired — no extra fields required.
#Notification: #Input & {
	hook_event_name: "Notification"
}
