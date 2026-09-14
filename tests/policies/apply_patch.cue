package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
	"github.com/srnnkls/fas/cue/path"
)

system_patch: {
	when: hook.#PreToolUse & tool.#ApplyPatch & path.#hasSystemTarget
	then: deny: {
		rule_id:  "system-patch"
		reason:   "System patch blocked"
		severity: "HIGH"
	}
}
