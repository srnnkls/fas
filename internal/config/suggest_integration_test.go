package config_test

import (
	"slices"
	"testing"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
)

func noteMessages(de *diag.DiagError) []string {
	out := make([]string, 0, len(de.D.Notes))
	for _, n := range de.D.Notes {
		out = append(out, n.Msg)
	}
	return out
}

func hasNote(de *diag.DiagError, want string) bool {
	return slices.Contains(noteMessages(de), want)
}

func TestLoadRules_QualifiedStdlibTypo_AttachesSuggestion(t *testing.T) {
	const src = `package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/agent"
)

orient: {
	when: hook.#SubagentStart & agent.#Explor
	then: inject: {
		rule_id: "x"
		channel: "agent"
		text:    "y"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "orient.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected error for typo'd agent.#Explor")
	}
	const want = "did you mean `agent.#Explore`?"
	found := false
	for _, de := range collectDiags(err) {
		if hasNote(de, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a suggestion note %q; notes were %v", want, allNotes(err))
	}
}

func TestLoadRules_QualifiedStdlibTypo_FarMiss_NoSuggestion(t *testing.T) {
	const src = `package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/agent"
)

orient: {
	when: hook.#SubagentStart & agent.#Zzzzzzzz
	then: inject: {
		rule_id: "x"
		channel: "agent"
		text:    "y"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "orient.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected error for typo'd agent.#Zzzzzzzz")
	}
	for _, de := range collectDiags(err) {
		if len(de.D.Notes) > 0 {
			t.Errorf("far miss must not attach a suggestion; got %v", noteMessages(de))
		}
	}
}

func TestLoadRules_BareUnboundTypo_AttachesSuggestion(t *testing.T) {
	const src = `package rules

_shared: {tool_name: "Bash"}

guard: {
	when: _shaerd
	then: deny: {
		rule_id: "x"
		reason:  "y"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "guard.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected error for unbound _shaerd")
	}
	const want = "did you mean `_shared`?"
	found := false
	for _, de := range collectDiags(err) {
		if hasNote(de, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a suggestion note %q; notes were %v", want, allNotes(err))
	}
}

func allNotes(err error) []string {
	diags := collectDiags(err)
	out := make([]string, 0, len(diags))
	for _, de := range diags {
		out = append(out, noteMessages(de)...)
	}
	return out
}
