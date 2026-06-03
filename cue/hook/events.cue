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

// #Agent provides matchers for the built-in Claude Code subagent types (per the
// docs: Explore, Plan, general-purpose). Compose them with the event, the same
// way tool.#isBash & friends compose:
//
//	when: hook.#SubagentStart & hook.#Agent.Explore
//
// Each constrains only agent_type, so the same matcher works for SubagentStart
// and SubagentStop. A typo'd member (hook.#Agent.Explor) is an "undefined field"
// the loader rejects, not a silent non-match. The event definitions keep
// agent_type an open string, so custom subagents (your own .claude/agents, task
// runners, …) still match via {agent_type: "your-agent"}.
#Agent: {
	Explore:        {agent_type: "Explore", ...}
	Plan:           {agent_type: "Plan", ...}
	GeneralPurpose: {agent_type: "general-purpose", ...}
}

// #KnownAgentType matches any built-in subagent — the disjunction of the #Agent
// matchers. Compose as hook.#SubagentStart & hook.#KnownAgentType.
#KnownAgentType: #Agent.Explore | #Agent.Plan | #Agent.GeneralPurpose

// #SubagentStart: a subagent is about to start — agent_type names the starting
// subagent. Target a specific kind with `& hook.#Agent.Explore` (built-ins) or
// `& {agent_type: "your-agent"}` for custom subagents.
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
