package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
	"github.com/srnnkls/fas/cue/bash"
	"github.com/srnnkls/fas/cue/path"
)

curl_pipe_system: {
	when: hook.#PreToolUse & tool.#Bash &
		(bash.#command & {#name: "tee"}) &
		path.#hasSystemInCommand
	then: deny: {
		rule_id:  "tee-system"
		reason:   "Writing to system paths via tee is blocked"
		severity: "CRITICAL"
	}
}
