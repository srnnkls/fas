// Package catalog is the canonical vocabulary fas matches against: the wire
// identities of the tools, subagent types, and hook events Claude Code
// dispatches. These are pure name tables — no wire-field binding; the wire
// layer (cue/hook event shapes, cue/tool matchers) references them. One name,
// one source of truth: a member typo (catalog.#ToolName.Bsh) is an undefined
// field the loader rejects, not a silent non-match. The values are Claude
// Code's own identities; a different harness would ship its own catalog.
package catalog

// #ToolName names the built-in tools a policy keys on. Custom MCP or skill
// tools are absent by design — they still match by their own tool_name.
#ToolName: {
	Agent:           "Agent"
	AskUserQuestion: "AskUserQuestion"
	Bash:            "Bash"
	Edit:            "Edit"
	Glob:            "Glob"
	Grep:            "Grep"
	MultiEdit:       "MultiEdit"
	NotebookEdit:    "NotebookEdit"
	Read:            "Read"
	TaskCreate:      "TaskCreate"
	TaskGet:         "TaskGet"
	TaskList:        "TaskList"
	TaskUpdate:      "TaskUpdate"
	TaskStop:        "TaskStop"
	TodoWrite:       "TodoWrite"
	WebFetch:        "WebFetch"
	WebSearch:       "WebSearch"
	Write:           "Write"
}

// #AgentType names the built-in Claude Code subagent types. Custom subagents
// still match by their own agent_type.
#AgentType: {
	Explore:        "Explore"
	Plan:           "Plan"
	GeneralPurpose: "general-purpose"
}

// #EventName names every hook event fas evaluates.
#EventName: {
	PreToolUse:       "PreToolUse"
	PostToolUse:      "PostToolUse"
	UserPromptSubmit: "UserPromptSubmit"
	Stop:             "Stop"
	SubagentStart:    "SubagentStart"
	SubagentStop:     "SubagentStop"
	Notification:     "Notification"
}
