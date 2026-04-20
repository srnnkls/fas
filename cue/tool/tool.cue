// Package tool defines per-tool matchers for the `tool_name` dispatched by
// the harness. New tool matchers (Write, Edit, etc.) get added here as the
// policy surface grows.
package tool

// #isBash: the invocation targets the Bash tool.
#isBash: {
	tool_name: "Bash"
	...
}

// Write-class command matchers — unify with #isBash to constrain the
// specific executable invoked inside the shell.

#isRm: {tool_input: command:       =~"^rm\\b", ...}
#isChmod: {tool_input: command:    =~"^chmod\\b", ...}
#isChown: {tool_input: command:    =~"^chown\\b", ...}
#isChgrp: {tool_input: command:    =~"^chgrp\\b", ...}
#isDd: {tool_input: command:       =~"^dd\\b", ...}
#isTruncate: {tool_input: command: =~"^truncate\\b", ...}
#isTee: {tool_input: command:      =~"^tee\\b", ...}
#isInstall: {tool_input: command:  =~"^install\\b", ...}
#isCp: {tool_input: command:       =~"^cp\\b", ...}
#isMv: {tool_input: command:       =~"^mv\\b", ...}
#isLn: {tool_input: command:       =~"^ln\\b", ...}
#isMkdir: {tool_input: command:    =~"^mkdir\\b", ...}
#isRmdir: {tool_input: command:    =~"^rmdir\\b", ...}
#isTouch: {tool_input: command:    =~"^touch\\b", ...}
