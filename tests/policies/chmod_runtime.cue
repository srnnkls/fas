package rules

import (
	"github.com/srnnkls/fas/cue/command"
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/path"
	"github.com/srnnkls/fas/cue/tool"
)

// Socket files and PID files in /run are owned by system daemons; widening
// their permissions can let an unprivileged process hijack a privileged socket
// (e.g. /run/docker.sock grants root-equivalent container control). The same
// risk applies to other system directories, so the matcher extends the system
// set with /run via #InCommandRe's #extra hook.
chmod_runtime_blocklist: {
	when: hook.#PreToolUse & tool.#Tool.Bash & (command.#commandRobust & {#name: "chmod"}) & {
		tool_input: command: (path.#InCommandRe & {
			#prefixes: path.#SystemPrefixes
			#extra: ["/run"]
		}).out
	}
	then: deny: {
		rule_id:  "chmod-runtime"
		reason:   "Changing permissions on runtime directories is blocked"
		severity: "HIGH"
	}
}
