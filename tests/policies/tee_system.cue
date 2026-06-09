package rules

import (
	"github.com/srnnkls/fas/cue/bash"
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/path"
	"github.com/srnnkls/fas/cue/tool"
)

// `tee` is the canonical vector for writing to privileged files without a
// redirect: `echo "..." | sudo tee /etc/sudoers.d/override` bypasses shell
// redirect restrictions. CRITICAL severity ensures this reason surfaces even
// when the generic system-path rules also fire.
tee_system: {
	when: hook.#PreToolUse & tool.#Bash & (bash.#commandOrRaw & {#name: "tee"}) & path.#hasSystemInCommand
	then: deny: {
		rule_id:  "tee-system-path"
		reason:   "Writing to system paths via tee is blocked"
		severity: "CRITICAL"
	}
}
