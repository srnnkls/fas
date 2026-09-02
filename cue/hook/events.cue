// Package hook defines the canonical names and per-event shapes for every
// hook Claude Code dispatches to fas. Definitions here are standalone
// constraints — they pin hook_event_name to a single literal and add per-event
// required fields without referencing the broader #Input schema, so the
// `hook` package stays free of a cycle against `fas`.
//
// Rule authors compose these with other sub-package constraints:
//
//	when: hook.#PreToolUse & tool.#Bash & path.#hasSystemTarget
package hook

import "github.com/srnnkls/fas/cue/catalog"

// #HookEventName enumerates every hook event fas evaluates — the disjunction of
// the catalog event identities. Retyping fas.#Input.hook_event_name against it
// turns typos like "PreToolUsex" into load-time failures instead of silent
// policy misses.
#HookEventName: or([for _, v in catalog.#EventName {v}])

#PermissionMode: or([for _, v in catalog.#PermissionMode {v}])
#EffortLevel: or([for _, v in catalog.#EffortLevel {v}])

// #Common carries the fields every hook event receives.
#Common: {
	session_id?:      string
	prompt_id?:       string
	transcript_path?: string
	cwd?:             string
	permission_mode?: #PermissionMode
	effort?: level?: #EffortLevel
	agent_id?:   string
	agent_type?: string
	...
}

// Claude Code sends agent_id exactly when the hook fires inside a subagent
// call; fas encodes the main thread as "" so both directions are a subsumable
// leaf constraint rather than a test for field absence.
#MainThread: {
	agent_id: ""
	...
}

#InSubagent: {
	agent_id: string & !=""
	...
}

// #PreToolUse: a tool invocation is about to run — tool_name must be present.
#PreToolUse: #Common & {
	hook_event_name: catalog.#EventName.PreToolUse
	tool_name:       string & !=""
	tool_use_id?:    string
	...
}

// #PostToolUse: a tool invocation just finished — tool_name is required and
// tool_response rides along at the top level so rules can inspect the result
// (e.g. tool_response.numFiles for an empty Grep).
#PostToolUse: #Common & {
	hook_event_name: catalog.#EventName.PostToolUse
	tool_name:       string & !=""
	tool_use_id?:    string
	tool_input?: {
		command?: string
		parsed?: {...}
		...
	}
	tool_response?: _
	...
}

// #UserPromptSubmit: the user submitted a prompt — prompt must be non-empty.
#UserPromptSubmit: #Common & {
	hook_event_name: catalog.#EventName.UserPromptSubmit
	prompt:          string & !=""
	...
}

// #Stop: the session is stopping — last_assistant_message carries the turn's
// final assistant text, which the transcript file may not have caught up to.
#Stop: #Common & {
	hook_event_name:         catalog.#EventName.Stop
	last_assistant_message?: string
	...
}

// #SubagentStart: a subagent is about to start — agent_type names the starting
// subagent. Target a specific kind with `& agent.#Explore` (built-ins) or
// `& {agent_type: "your-agent"}` for custom subagents.
#SubagentStart: #Common & {
	hook_event_name: catalog.#EventName.SubagentStart
	agent_type?:     string
	...
}

// #SubagentStop: a subagent just finished — agent_type names the subagent that
// stopped, letting rules react to one kind of subagent completing.
#SubagentStop: #Common & {
	hook_event_name:         catalog.#EventName.SubagentStop
	agent_type?:             string
	last_assistant_message?: string
	...
}

// #Notification: a harness-level notification fired — no extra fields required.
#Notification: #Common & {
	hook_event_name: catalog.#EventName.Notification
	...
}
