package rules

import (
	"github.com/srnnkls/quae/cue/hook"
	"github.com/srnnkls/quae/cue/path"
	"github.com/srnnkls/quae/cue/tool"
)

// Moving or renaming files under /var/log destroys the audit trail that
// incident response depends on. An attacker clearing /var/log/auth.log before
// exfiltrating data is a classic cover-your-tracks step.
mv_var_log: {
	when: hook.#PreToolUse & tool.#isBash & tool.#isMv & {
		tool_input: command: (path.#InCommandRe & {#prefixes: ["/var/log"]}).out
	}
	then: deny: {
		rule_id:  "mv-var-log"
		reason:   "Moving system log files conceals audit evidence"
		severity: "HIGH"
	}
}
