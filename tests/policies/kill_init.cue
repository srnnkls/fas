package rules

import (
	"list"

	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
	"github.com/srnnkls/fas/cue/bash"
)

// A single rule that denies two closely related shapes via struct-level `|`:
// either a `kill` naming PID 1 directly, or `killall` naming the init process
// supervisor (`systemd` or `init`). Both vectors terminate PID 1 — the one
// process whose death takes the whole system with it — so a shared rule_id
// and reason keep the policy surface legible while still allowing harmless
// signal delivery to ordinary processes.
//
// `|` binds looser than `&`, so the outer `hook.#PreToolUse & tool.#Bash`
// is repeated on each disjunct to keep the grouping explicit and readable.
kill_init: {
	when: {
		hook.#PreToolUse
		tool.#Bash
		bash.#command & {#name: "kill"}
		tool_input: parsed: targets: list.MatchN(>0, "1")
	} | {
		hook.#PreToolUse
		tool.#Bash
		bash.#command & {#name: "killall"}
		tool_input: parsed: targets: list.MatchN(>0, "systemd" | "init")
	}
	then: deny: {
		rule_id:  "kill-init"
		reason:   "Refusing to signal the init process"
		severity: "HIGH"
	}
}
