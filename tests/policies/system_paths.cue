package rules

import (
	"list"
	"strings"
)

_SystemPrefixes: ["/etc", "/sys", "/proc", "/boot", "/dev"]
_systemTarget:   =~"^(\(strings.Join(_SystemPrefixes, "|")))"

// Primary match: parsed.targets references a system prefix. Covers simple
// commands and compound forms whose AST walker surfaces the path in targets
// (e.g. `echo start && rm -rf /etc/passwd` — both commands' args are walked).
rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: list.MatchN(>0, _systemTarget)
	}
	then: deny: {
		rule_id:  "system-path"
		reason:   "System path blocked"
		severity: "HIGH"
	}
}
