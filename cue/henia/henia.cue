package henia

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

#Tier: "project" | "global"

#Preload: hook.#PreToolUse & tool.#Bash & {
	henia: {
		skill:   string
		source:  string
		tier:    #Tier
		caller?: string
		...
	}
	...
}

#Project: #Preload & {henia: tier: "project", ...}
#Global: #Preload & {henia: tier: "global", ...}

#Source: #Preload & {
	#source: string
	henia: source: #source
	...
}

#Skill: #Preload & {
	#skill: string
	henia: skill: #skill
	...
}

#Caller: #Preload & {
	#caller: string
	henia: caller: #caller
	...
}
