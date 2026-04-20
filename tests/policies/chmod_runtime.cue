package rules

import (
	"github.com/srnnkls/quae/cue/hook"
	"github.com/srnnkls/quae/cue/path"
	"github.com/srnnkls/quae/cue/tool"
)

// Socket files and PID files in /run are owned by system daemons; widening
// their permissions can let an unprivileged process hijack a privileged socket
// (e.g. /run/docker.sock grants root-equivalent container control). The same
// risk applies to other system directories, so the matcher extends the system
// set with /run via #InCommandRe's #extra hook.
rule: {
	when: hook.#PreToolUse & tool.#isBash & tool.#isChmod & {
		tool_input: command: (path.#InCommandRe & {
			#prefixes: path.#SystemPrefixes
			#extra:    ["/run"]
		}).out
	}
	then: deny: {
		rule_id:  "chmod-runtime"
		reason:   "Changing permissions on runtime directories is blocked"
		severity: "HIGH"
	}
}
