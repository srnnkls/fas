package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

// A single rule that denies two closely related shapes via struct-level `|`:
// either a `kill` naming PID 1 directly, or `killall` naming the init process
// supervisor (`systemd` or `init`). Both vectors terminate PID 1 — the one
// process whose death takes the whole system with it — so a shared rule_id
// and reason keep the policy surface legible while still allowing harmless
// signal delivery to ordinary processes.
//
// `|` binds looser than `&`, so the outer `hook.#PreToolUse & tool.#Tool.Bash`
// is repeated on each disjunct to keep the grouping explicit and readable.
kill_init: {
	when: {
		hook.#PreToolUse
		tool.#Tool.Bash
		tool_input: command: =~"^kill\\s+(-[A-Z0-9]+\\s+)?1(\\s|$)"
	} | {
		hook.#PreToolUse
		tool.#Tool.Bash
		tool_input: command: =~"^killall\\s+(-[A-Z0-9]+\\s+)?(systemd|init)(\\s|$)"
	}
	then: deny: {
		rule_id:  "kill-init"
		reason:   "Refusing to signal the init process"
		severity: "HIGH"
	}
}
