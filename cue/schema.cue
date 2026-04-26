// Package fas holds the core schema types rule loaders and signal
// evaluators consume. Per-tool matchers, flag constraints, and hook-event
// shapes live in sub-packages (cue/hook, cue/tool, cue/path, cue/flag, ...).
//
// Rule authors no longer import this package directly — they import each
// sub-package they need. The file stays in the `fas` package so
// ValidateInput and LoadRules can keep compiling it via `cue.CompileBytes`
// without relying on the module loader.
package fas

// #HookEventName enumerates every hook event fas evaluates. Retyping
// #Input.hook_event_name against this disjunction turns typos like
// "PreToolUsex" into load-time failures instead of silent policy misses.
//
// The sub-package `cue/hook` re-declares the same set so rule authors can
// reference it without pulling in the core-schema package. Keeping the copy
// here standalone avoids a module-loader dependency for the in-process
// schema cache.
#HookEventName: "PreToolUse" | "PostToolUse" | "UserPromptSubmit" | "Stop" | "SubagentStart" | "Notification"

#Input: {
	hook_event_name: #HookEventName
	tool_name?:      string
	tool_input?: {
		command?: string
		parsed?:  #Parsed
		...
	}
	session_id?: string
	cwd?:        string
	signals?: {[string]: #SignalResult}
	...
}

// #Parsed is the canonical namespace for the preprocessor-enriched view of a
// tool invocation. The list-shaped fields are intentionally typed as `_`
// rather than `[...string]` because CUE's evaluator eagerly concretises an
// open list's default to `[]` when a definition is referenced without a
// value, and any downstream constraint like `list.MatchN(>0, ...)` (added by
// sub-package composites such as `path.#hasSystemTarget`) would then fail
// against that empty default at rule-load time — long before a real input
// arrives. Rule authors or the sub-packages tighten the type at the
// composition point.
#Parsed: {
	actions?:    _
	targets?:    _
	flags?:      _
	attributes?: {...}
	...
}

#SignalResult: {
	ok:    bool
	data?: _
	err?:  string
}

#Meta: {
	requires?: [...string]
}

#Deny: deny: {
	rule_id:  string
	reason:   string
	severity: *"HIGH" | "CRITICAL" | "MEDIUM" | "LOW"
}

#Ask: ask: {
	rule_id:  string
	reason:   string
	question: string
}

#Allow: allow: true

#Inject: inject: {
	rule_id:  string
	priority: *50 | int & >=1 & <=100
	channel:  *"agent" | "user"
	text:     string
	tags?: [...string]
}

#Modify: modify: {
	rule_id:       string
	reason:        string
	updated_input: _
	priority:      *50 | int & >=1 & <=100
	mode:          *"confirm" | "silent"
}

#Action: #Deny | #Ask | #Modify | #Inject | #Allow

#Rule: {
	when:  {...}
	then?: #Action
	meta?: #Meta
}

// #Rules is a `[string]: #Rule` shape for ad-hoc CUE-level validation
// (schema tests, editor tooling, external checks). The loader never sees a
// `Rules:` envelope — it iterates every top-level non-hidden field and
// unifies each against #Rule directly.
#Rules: [string]: #Rule
