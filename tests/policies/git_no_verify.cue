package rules

import (
	"list"

	"github.com/srnnkls/quae/cue:quae"
)

// `-n` is the short form of `--no-verify` only for `git commit` and
// `git merge`. For `git push`, `-n` means `--dry-run` (unrelated to hook
// bypass) and must not be denied here.
rule: {
	when: quae.#PreToolUse & quae.#isBash & {
		tool_input: {
			command: =~"^git\\s+(commit|merge)\\b"
			parsed: flags: list.MatchN(>0, =~"^(--no-verify|-n)$")
		}
	}
	then: deny: {
		rule_id:  "git-no-verify"
		reason:   "Git --no-verify is not permitted; commit/push hooks must run"
		severity: "HIGH"
	}
}
