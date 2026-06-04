// Package catalog is the harness-agnostic vocabulary fas matches against: the
// canonical identities of the tools, subagent types, and hook events Claude
// Code dispatches. These are pure name tables — no wire-field binding. The
// wire layer (cue/hook event shapes, cue/tool matchers) references them, so a
// renamed field or a second harness reshapes the binding while the names here
// stay put. The tables mirror the published tools reference, so a member typo
// (catalog.#ToolName.Bsh) is an undefined field the loader rejects.
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
