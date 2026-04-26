// Package hook defines the canonical names and per-event shapes for every
// hook Claude Code dispatches to fas. Definitions here are standalone
// constraints — they pin hook_event_name to a single literal and add per-event
// required fields without referencing the broader #Input schema, so the
// `hook` package stays free of a cycle against `fas`.
//
// Rule authors compose these with other sub-package constraints:
//
//	when: hook.#PreToolUse & tool.#isBash & path.#hasSystemTarget
package hook

// #HookEventName enumerates every hook event fas evaluates. Retyping
// fas.#Input.hook_event_name against this disjunction turns typos like
// "PreToolUsex" into load-time failures instead of silent policy misses.
#HookEventName: "PreToolUse" | "PostToolUse" | "UserPromptSubmit" | "Stop" | "SubagentStart" | "Notification"

// #PreToolUse: a tool invocation is about to run — tool_name must be present.
#PreToolUse: {
	hook_event_name: "PreToolUse"
	tool_name:       string & !=""
	...
}

// #PostToolUse: a tool invocation just finished — tool_name is required and
// tool_response rides along inside tool_input so signals can inspect the result.
#PostToolUse: {
	hook_event_name: "PostToolUse"
	tool_name:       string & !=""
	tool_input?: {
		command?:       string
		parsed?:        {...}
		tool_response?: _
		...
	}
	...
}

// #UserPromptSubmit: the user submitted a prompt — prompt must be non-empty.
#UserPromptSubmit: {
	hook_event_name: "UserPromptSubmit"
	prompt:          string & !=""
	...
}

// #Stop: the session is stopping — no extra fields beyond the event name.
#Stop: {
	hook_event_name: "Stop"
	...
}

// #SubagentStart: a subagent is about to start — no extra fields required.
#SubagentStart: {
	hook_event_name: "SubagentStart"
	...
}

// #Notification: a harness-level notification fired — no extra fields required.
#Notification: {
	hook_event_name: "Notification"
	...
}
