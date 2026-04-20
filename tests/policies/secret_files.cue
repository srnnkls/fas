package rules

import "github.com/srnnkls/quae/cue:quae"

// `git add` of likely-secret paths is almost always a mistake. The regex
// anchors on the `git add` verb and matches against the raw command string
// so the intent stays obvious next to the verb for maintainers skimming the
// rule, even though parsed.targets would also surface the filenames.
rule: {
	when: quae.#PreToolUse & quae.#isBash & {
		tool_input: command: =~"^git\\s+add\\s+.*(\\.env\\b|credentials\\.json\\b|id_rsa\\b)"
	}
	then: deny: {
		rule_id:  "secret-files"
		reason:   "Refusing to stage a likely secret file"
		severity: "HIGH"
	}
}
