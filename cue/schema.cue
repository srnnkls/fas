package quae

#Input: {
	hook_event_name: string
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

#Parsed: {
	actions?:    [...string]
	targets?:    [...string]
	flags?:      [...string]
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
