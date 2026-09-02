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
	effort?: {
		level?: #EffortLevel
		...
	}
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

#NotificationType: or([for _, v in catalog.#NotificationType {v}])

// #BackgroundTask is one entry of the background_tasks array Stop and
// SubagentStop carry. Only id, type, status, and description are always
// present; the rest are per-kind, so a rule keying on `command` implicitly
// selects shell tasks.
#BackgroundTask: {
	id:           string
	type:         string
	status:       string
	description?: string
	command?:     string
	agent_type?:  string
	server?:      string
	tool?:        string
	name?:        string
	...
}

// #SessionCron is one entry of the session_crons array, sourced from
// CronCreate, ScheduleWakeup, and /loop.
#SessionCron: {
	id:         string
	schedule?:  string
	recurring?: bool
	prompt?:    string
	...
}

// #Stopping carries the fields Stop and SubagentStop share. Both arrays arrive
// empty rather than absent when the task registry is reachable and nothing is
// in flight, which is why `background_tasks: []` is a usable match for "the
// session is really done" and absence is not.
#Stopping: {
	stop_hook_active?:       bool
	last_assistant_message?: string
	background_tasks?: [...#BackgroundTask]
	session_crons?: [...#SessionCron]
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
	duration_ms?:   int
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
// stop_hook_active is true when Claude Code is already continuing because of a
// stop hook; a rule that blocks without checking it can loop until Claude Code
// overrides it after 8 consecutive blocks.
#Stop: #Common & #Stopping & {
	hook_event_name: catalog.#EventName.Stop
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
//
// agent_id is the stopping subagent's, so #MainThread never matches this event,
// and its additionalContext lands in that subagent's context rather than the
// orchestrator's. To react in the parent, key on #PostToolUse & tool.#Agent.
// background_tasks and session_crons are scoped to the parent session.
#SubagentStop: #Common & #Stopping & {
	hook_event_name:        catalog.#EventName.SubagentStop
	agent_type?:            string
	agent_transcript_path?: string
	...
}

// #Notification: a harness-level notification fired. notification_type is the
// value Claude Code's own matcher filters on.
#Notification: #Common & {
	hook_event_name:    catalog.#EventName.Notification
	message?:           string
	title?:             string
	notification_type?: #NotificationType
	...
}
