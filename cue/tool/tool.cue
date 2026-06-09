// Package tool binds the built-in tool identities in cue/catalog to the harness
// wire field `tool_name`. Each member pins tool_name to one catalog identity, so
// a rule matches a tool by composing the event with it — hook.#PreToolUse &
// tool.#Bash. Matchers over the *command* a Bash call runs (rm, tee, …) live in
// cue/bash.
package tool

import "github.com/srnnkls/fas/cue/catalog"

// _byName binds every catalog tool identity to its tool_name constraint. The
// per-tool definitions alias into it and #Known disjuncts over it, so the
// catalog stays the single source of the member set: a dropped tool surfaces as
// an undefined-field load error here, not a silent non-match.
_byName: {
	for k, v in catalog.#ToolName {(k): {tool_name: v, ...}}
}

#Agent:           _byName.Agent
#AskUserQuestion: _byName.AskUserQuestion
#Bash:            _byName.Bash
#Edit:            _byName.Edit
#Glob:            _byName.Glob
#Grep:            _byName.Grep
#MultiEdit:       _byName.MultiEdit
#NotebookEdit:    _byName.NotebookEdit
#Read:            _byName.Read
#TaskCreate:      _byName.TaskCreate
#TaskGet:         _byName.TaskGet
#TaskList:        _byName.TaskList
#TaskUpdate:      _byName.TaskUpdate
#TaskStop:        _byName.TaskStop
#TodoWrite:       _byName.TodoWrite
#WebFetch:        _byName.WebFetch
#WebSearch:       _byName.WebSearch
#Write:           _byName.Write

// #Known matches any built-in tool — the disjunction of the members above.
// Compose as hook.#PreToolUse & tool.#Known. The event shapes keep tool_name an
// open string, so custom MCP/skill tools still match via {tool_name: "your-tool"}.
#Known: or([for _, m in _byName {m}])
