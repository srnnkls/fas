package main

import (
	"strings"
	"testing"
)

const heniaRulesSrc = `package rules

import (
	"github.com/srnnkls/fas/cue/bash"
	"github.com/srnnkls/fas/cue/henia"
)

project_echo: {
	when: henia.#Project & (bash.#command & {#name: "echo"})
	then: deny: {rule_id: "project-echo", reason: "project echo", severity: "HIGH"}
}
source_date: {
	when: henia.#Source & {#source: "tropos"} & (bash.#command & {#name: "date"})
	then: ask: {rule_id: "tropos-date", reason: "date?", question: "run?"}
}
skill_status: {
	when: henia.#Skill & {#skill: "status"} & henia.#Global & (bash.#command & {#name: "git"})
	then: modify: {rule_id: "git-locks", reason: "no locks", mode: "silent", updated_input: command: "git --no-optional-locks status"}
}
caller_codex: {
	when: henia.#Caller & {#caller: "codex"} & (bash.#command & {#name: "uname"})
	then: deny: {rule_id: "codex-uname", reason: "codex uname", severity: "LOW"}
}
`

func TestHeniaHarness(t *testing.T) {
	a, ok := selectAdapter("henia")
	if !ok || a.Name() != "henia" {
		t.Fatal("henia adapter not registered")
	}
	empty := emptyRulesDir(t)
	project := writeRuleFiles(t, map[string]string{"henia.cue": heniaRulesSrc})
	preload := func(command, source, tier, caller string) []byte {
		return []byte(`{"command":"` + command + `","skill":"status","source":"` + source + `","tier":"` + tier + `","caller":"` + caller + `"}`)
	}
	for _, tc := range []struct {
		name  string
		input []byte
		want  string
	}{
		{"project tier denies", preload("echo hi", "project", "project", "claude"), `{"decision":"deny","rule":"project-echo","reason":"project echo"}`},
		{"global tier allows", preload("echo hi", "tropos", "global", "claude"), `{"decision":"allow"}`},
		{"ask denies", preload("date", "tropos", "global", "claude"), `{"decision":"deny","rule":"tropos-date","reason":"date?\nrun?"}`},
		{"other source allows", preload("date", "other", "global", "claude"), `{"decision":"allow"}`},
		{"silent modify rewrites", preload("git status", "tropos", "global", "claude"), `{"decision":"allow","command":"git --no-optional-locks status"}`},
		{"modify needs global tier", preload("git status", "project", "project", "claude"), `{"decision":"allow"}`},
		{"caller matches", preload("uname", "tropos", "global", "codex"), `{"decision":"deny","rule":"codex-uname","reason":"codex uname"}`},
		{"other caller allows", preload("uname", "tropos", "global", "claude"), `{"decision":"allow"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runCLI(t, tc.input, "eval", "--harness", "henia", "--config", project, "--global-config", empty)
			if res.exit != 0 || string(res.stdout) != tc.want {
				t.Fatalf("exit=%d stdout=%s stderr=%s", res.exit, res.stdout, res.stderr)
			}
		})
	}
	agent := []byte(`{"hook_event_name":"PreToolUse","tool_name":"bash","tool_input":{"command":"echo hi"}}`)
	res := runCLI(t, agent, "eval", "--harness", "pi", "--config", project, "--global-config", empty)
	if res.exit != 0 || string(res.stdout) != "{}" {
		t.Fatalf("preload rule matched an agent call: %s", res.stdout)
	}
	res = runCLI(t, preload("echo hi", "project", "project", ""), "eval", "--harness", "bogus")
	if res.exit == 0 || !strings.Contains(string(res.stderr), "henia") {
		t.Fatalf("unknown harness: %s", res.stderr)
	}
}
