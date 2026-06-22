package rules

import "github.com/srnnkls/fas/cue/hook"

universe_or_doc_tools: {
	when: hook.#PreToolUse & {tool_name: or(["WebFetch", "WebSearch"])}
	then: deny: {
		rule_id:  "universe-or-doc-tools"
		reason:   "Documentation lookup tools are gated by an or() builtin"
		severity: "LOW"
	}
}
