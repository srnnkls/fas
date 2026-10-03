package rules

import (
	"github.com/srnnkls/fas/cue/bash"
	"github.com/srnnkls/fas/cue/henia"
)

project_preload_fetch: {
	when: henia.#Project & (bash.#command & {#name: "curl"})
	then: deny: {
		rule_id:  "henia-project-fetch"
		reason:   "Project preloads may not fetch URLs"
		severity: "HIGH"
	}
}
