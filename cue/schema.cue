package quae

// #HookEventName enumerates every hook event quae evaluates. Retyping
// #Input.hook_event_name against this disjunction turns typos like
// "PreToolUsex" into load-time failures instead of silent policy misses.
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
// stdlib composites such as `#hasSystemTarget`) would then fail against that
// empty default at rule-load time — long before a real input arrives. Rule
// authors or the stdlib tighten the type at the composition point.
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
