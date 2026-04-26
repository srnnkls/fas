package rules

import (
	"list"

	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

// Staging `.env`, credential JSON, or SSH private keys into git is almost
// always a mistake. The target regex recognizes `.env[.suffix]`, any file
// whose basename contains "credentials" with a recognized suffix, and the
// canonical SSH private-key names (id_rsa, id_ed25519, id_dsa, id_ecdsa).
secret_files: {
	when: hook.#PreToolUse & tool.#isBash & {
		tool_input: {
			command: =~"^git\\s+add\\b"
			parsed: targets: list.MatchN(>0, =~"(^|/)(\\.env(\\..+)?|.*credentials\\.(json|ya?ml|toml|env)|id_(rsa|ed25519|dsa|ecdsa))$")
		}
	}
	then: deny: {
		rule_id:  "secret-files"
		reason:   "Refusing to stage a likely secret file"
		severity: "HIGH"
	}
}
