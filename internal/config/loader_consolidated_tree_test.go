package config_test

import (
	"path/filepath"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// CRP-011 — no per-batch guard loads a tree that simultaneously exercises a
// root rule, a sibling `#def` package consumed via cross-package import, and an
// in-package cross-file `_helper`; this proves those features compose.
func TestLoadRules_ConsolidatedTree_ComposesFeatures(t *testing.T) {
	root := t.TempDir()

	mkSubdirRuleFile(t, root, "root.cue", `package rules

root_guard: {
	when: {hook_event_name: "PreToolUse", tool_name: "Read"}
	then: deny: {
		rule_id: "root-guard"
		reason:  "root flat rule"
	}
}
`)

	mkSubdirRuleFile(t, root, "schema/base.cue", `package schema

#Base: {
	hook_event_name: "PreToolUse"
	tool_name:       string
}
`)

	mkSubdirRuleFile(t, root, "security/helpers.cue", `package rules

_bashGuard: {tool_name: "Bash"}
`)
	mkSubdirRuleFile(t, root, "security/git.cue", `package rules

import "fas.local/rules/schema"

bash_guard: {
	when: schema.#Base & _bashGuard
	then: deny: {
		rule_id: "bash-guard"
		reason:  "schema #def import + cross-file helper compose"
	}
}
`)

	// workflow/ package: an independent rule in its own package.
	mkSubdirRuleFile(t, root, "workflow/deploy.cue", `package rules

deploy_gate: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: ask: {
		rule_id:  "deploy-gate"
		reason:   "workflow package rule"
		question: "deploy?"
	}
}
`)

	rules, err := config.LoadRules(root)
	if err != nil {
		t.Fatalf("rich tree must load (cross-pkg import + cross-file helper compose), got: %v", err)
	}

	// Order is CompareModulePath: dir-lexical ("." < security < workflow) then
	// basename; schema/ holds only a #def so it contributes no rule.
	wantIDs := []string{"root-guard", "bash-guard", "deploy-gate"}
	if len(rules) != len(wantIDs) {
		got := make([]string, len(rules))
		for i := range rules {
			if rules[i].Then != nil {
				got[i] = rules[i].Then.RuleID
			}
		}
		t.Fatalf("rule set = %d %v, want %d %v", len(rules), got, len(wantIDs), wantIDs)
	}
	for i, want := range wantIDs {
		got := ""
		if rules[i].Then != nil {
			got = rules[i].Then.RuleID
		}
		if got != want {
			t.Errorf("rules[%d].RuleID = %q, want %q (full order matters)", i, got, want)
		}
	}

	byID := rulesByID(rules)

	bg, ok := byID["bash-guard"]
	if !ok {
		t.Fatalf("bash-guard missing: cross-package import + cross-file helper did not compose")
	}
	if bg.Then == nil || bg.Then.Kind != config.ActionDeny {
		t.Errorf("bash-guard must decode a deny action, got %+v", bg.Then)
	}

	wantRel := map[string]string{
		"root-guard":  "root.cue",
		"bash-guard":  filepath.Join("security", "git.cue"),
		"deploy-gate": filepath.Join("workflow", "deploy.cue"),
	}
	wantSource := map[string]string{
		"root-guard":  filepath.ToSlash(filepath.Join(root, "root.cue")) + ":root_guard",
		"bash-guard":  filepath.ToSlash(filepath.Join(root, "security", "git.cue")) + ":bash_guard",
		"deploy-gate": filepath.ToSlash(filepath.Join(root, "workflow", "deploy.cue")) + ":deploy_gate",
	}
	for id, r := range byID {
		if r.ModuleRelPath != wantRel[id] {
			t.Errorf("%s.ModuleRelPath = %q, want %q", id, r.ModuleRelPath, wantRel[id])
		}
		if filepath.IsAbs(r.ModuleRelPath) {
			t.Errorf("%s.ModuleRelPath = %q must be relative", id, r.ModuleRelPath)
		}
		if r.Source != wantSource[id] {
			t.Errorf("%s.Source = %q, want %q", id, r.Source, wantSource[id])
		}
	}
}
