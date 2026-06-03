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
#HookEventName: "PreToolUse" | "PostToolUse" | "UserPromptSubmit" | "Stop" | "SubagentStart" | "SubagentStop" | "Notification"

// #PreToolUse: a tool invocation is about to run — tool_name must be present.
#PreToolUse: {
	hook_event_name: "PreToolUse"
	tool_name:       string & !=""
	...
}

// #PostToolUse: a tool invocation just finished — tool_name is required and
// tool_response rides along at the top level so rules can inspect the result
// (e.g. tool_response.numFiles for an empty Grep).
#PostToolUse: {
	hook_event_name: "PostToolUse"
	tool_name:       string & !=""
	tool_input?: {
		command?: string
		parsed?:  {...}
		...
	}
	tool_response?: _
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

// #Agent names the built-in Claude Code subagent types (per the docs: Explore,
// Plan, general-purpose). Reference these — e.g. agent_type: hook.#Agent.Explore
// — for a canonical, discoverable vocabulary: a typo'd member like
// hook.#Agent.Explor is an "undefined field" the loader rejects, not a bare
// string literal that silently never matches. The event definitions keep
// agent_type an open string, so custom subagents (your own .claude/agents, task
// runners, …) still match by name.
#Agent: {
	Explore:        "Explore"
	Plan:           "Plan"
	GeneralPurpose: "general-purpose"
}

// #KnownAgentType is the disjunction of the built-in subagent types, for rules
// that want to match "any built-in agent". Deliberately NOT used to type the
// agent_type field, which stays an open string so custom subagents still match.
#KnownAgentType: #Agent.Explore | #Agent.Plan | #Agent.GeneralPurpose

// #SubagentStart: a subagent is about to start — agent_type names the starting
// subagent. Target a specific kind with hook.#Agent.Explore (built-ins) or a
// bare string for custom subagents.
#SubagentStart: {
	hook_event_name: "SubagentStart"
	agent_type?:     string
	...
}

// #SubagentStop: a subagent just finished — agent_type names the subagent that
// stopped, letting rules react to one kind of subagent completing.
#SubagentStop: {
	hook_event_name: "SubagentStop"
	agent_type?:     string
	...
}

// #Notification: a harness-level notification fired — no extra fields required.
#Notification: {
	hook_event_name: "Notification"
	...
}
