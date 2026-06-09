// Package agent binds the built-in subagent identities in cue/catalog to the
// harness wire field `agent_type`. Each member pins agent_type to one catalog
// identity, so a rule matches a subagent by composing the event with it —
// hook.#SubagentStart & agent.#Explore. The same matcher works for SubagentStart
// and SubagentStop.
package agent

import "github.com/srnnkls/fas/cue/catalog"

// _byName binds every catalog subagent identity to its agent_type constraint.
// The per-agent definitions alias into it and #Known disjuncts over it, so the
// catalog stays the single source of the member set: a dropped subagent surfaces
// as an undefined-field load error here, not a silent non-match.
_byName: {
	for k, v in catalog.#AgentType {(k): {agent_type: v, ...}}
}

#Explore:        _byName.Explore
#Plan:           _byName.Plan
#GeneralPurpose: _byName.GeneralPurpose

// #Known matches any built-in subagent — the disjunction of the members above.
// The event shapes keep agent_type an open string, so custom subagents still
// match via {agent_type: "your-agent"}.
#Known: or([for _, m in _byName {m}])
