package rules

import "github.com/srnnkls/quae/cue:quae"

// Primary match: parsed.targets references a system prefix. Covers simple
// commands and compound forms whose AST walker surfaces the path in targets
// (e.g. `echo start && rm -rf /etc/passwd` — both commands' args are walked).
rule: {
	when: quae.#PreToolUse & quae.#isBash & quae.#hasSystemTarget
	then: deny: {
		rule_id:  "system-path"
		reason:   "System path blocked"
		severity: "HIGH"
	}
}
