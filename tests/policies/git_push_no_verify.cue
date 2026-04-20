package rules

import (
	"list"

	"github.com/srnnkls/quae/cue/hook"
	"github.com/srnnkls/quae/cue/tool"
)

// `git push` has no `-n` alias for `--no-verify` — there, `-n` means
// `--dry-run`. Match the long form only.
rule: {
	when: hook.#PreToolUse & tool.#isBash & {
		tool_input: {
			command: =~"^git\\s+push\\b"
			parsed: flags: list.MatchN(>0, =~"^--no-verify$")
		}
	}
	then: deny: {
		rule_id:  "git-push-no-verify"
		reason:   "Git --no-verify is not permitted; commit/push hooks must run"
		severity: "HIGH"
	}
}
