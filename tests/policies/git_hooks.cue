package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
	"github.com/srnnkls/fas/cue/bash"
	"github.com/srnnkls/fas/cue/flag"
)

// `-n` is the short form of `--no-verify` only for `git commit` and
// `git merge`. For `git push`, `-n` means `--dry-run` (unrelated to hook
// bypass) and must not be denied here.
commit_merge: {
	when: hook.#PreToolUse & tool.#Bash & (bash.#subcommand & {#of: "git", #name: "commit" | "merge"}) & (flag.#hasOption & flag.opt.noVerifyCommit)
	then: deny: {
		rule_id:  "git-no-verify"
		reason:   "Git --no-verify is not permitted; commit/push hooks must run"
		severity: "HIGH"
	}
}

// `git push` has no `-n` alias for `--no-verify` — there, `-n` means
// `--dry-run`. Match the long form only.
push: {
	when: hook.#PreToolUse & tool.#Bash & (bash.#subcommand & {#of: "git", #name: "push"}) & (flag.#hasOption & flag.opt.noVerify)
	then: deny: {
		rule_id:  "git-push-no-verify"
		reason:   "Git --no-verify is not permitted; commit/push hooks must run"
		severity: "HIGH"
	}
}
