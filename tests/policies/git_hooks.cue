package rules

import (
	"list"

	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

// `-n` is the short form of `--no-verify` only for `git commit` and
// `git merge`. For `git push`, `-n` means `--dry-run` (unrelated to hook
// bypass) and must not be denied here.
commit_merge: {
	when: hook.#PreToolUse & tool.#Tool.Bash & {
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

// `git push` has no `-n` alias for `--no-verify` — there, `-n` means
// `--dry-run`. Match the long form only.
push: {
	when: hook.#PreToolUse & tool.#Tool.Bash & {
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
