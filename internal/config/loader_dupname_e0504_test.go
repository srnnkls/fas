package config_test

import (
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// TestLoadRules_DuplicateRuleName_EmitsE0504 pins that the cross-file
// duplicate-rule-name guard surfaces as a structured E0504 diagnostic naming
// both offending files, not the interim plain fmt.Errorf string.
func TestLoadRules_DuplicateRuleName_EmitsE0504(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

git_x: {
	when: {x: 1}
	then: deny: {
		rule_id: "a"
		reason:  "a"
	}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

git_x: {
	when: {x: 2}
	then: deny: {
		rule_id: "b"
		reason:  "b"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected duplicate rule name across files to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0504" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0504")
	}

	msg := err.Error()
	if !strings.Contains(msg, "a.cue") {
		t.Errorf("E0504 error must name `a.cue`; got: %s", msg)
	}
	if !strings.Contains(msg, "b.cue") {
		t.Errorf("E0504 error must name `b.cue`; got: %s", msg)
	}
}

// TestLoadRules_AliasedRuleLabel_Extracted pins that a field-alias label
// (`X="git_x": {...}`, an *ast.Alias) is classified as a rule candidate and
// extracted, not silently dropped from LoadRules output.
func TestLoadRules_AliasedRuleLabel_Extracted(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

X="git_x": {
	when: {x: 1}
	then: deny: {
		rule_id: "gx"
		reason:  "r"
	}
}

_alias_ref: X.then.deny.rule_id
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("aliased rule label must load without error; got: %v", err)
	}

	var found *config.Rule
	for i := range rules {
		if rules[i].Then != nil && rules[i].Then.RuleID == "gx" {
			found = &rules[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("aliased rule (rule_id %q) was dropped from LoadRules output; got %d rules", "gx", len(rules))
	}
	if !strings.Contains(found.Source, "git_x") {
		t.Errorf("rule Source must name the resolved label %q; got: %s", "git_x", found.Source)
	}
}

// TestLoadRules_AliasedDuplicateRuleName_EmitsE0504 pins that an aliased
// duplicate of a plain rule (`Y="git_x"` shadowing plain `git_x`) is caught by
// the cross-file duplicate guard as E0504 rather than escaping dedup.
func TestLoadRules_AliasedDuplicateRuleName_EmitsE0504(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

git_x: {
	when: {x: 1}
	then: deny: {
		rule_id: "a"
		reason:  "a"
	}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

Y="git_x": {
	when: {x: 2}
	then: deny: {
		rule_id: "b"
		reason:  "b"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected aliased duplicate rule name across files to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0504" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0504")
	}

	msg := err.Error()
	if !strings.Contains(msg, "a.cue") {
		t.Errorf("E0504 error must name `a.cue`; got: %s", msg)
	}
	if !strings.Contains(msg, "b.cue") {
		t.Errorf("E0504 error must name `b.cue`; got: %s", msg)
	}
}

// TestLoadRules_DuplicateRuleName_SharedHelpersExempt guards that hidden
// helpers (`_x`) and definitions (`#X`) shared across files merge cleanly and
// are never treated as duplicate rules — only plain top-level rule labels are.
func TestLoadRules_DuplicateRuleName_SharedHelpersExempt(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

_helper: "blocked"

#Def: {hook_event_name: "PreToolUse"}

alpha: {
	when: #Def
	then: deny: {
		rule_id: "r-alpha"
		reason:  _helper
	}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

_helper: "blocked"

#Def: {hook_event_name: "PreToolUse"}

beta: {
	when: #Def
	then: deny: {
		rule_id: "r-beta"
		reason:  _helper
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("shared hidden helper / #Def across files must not trip the duplicate guard; got: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 distinct rules (alpha + beta), got %d", len(rules))
	}
}

// TestLoadRules_DuplicateRuleName_ComprehensionNotCandidate guards that a
// top-level comprehension (no plain ident label) across files is not a
// rule-shaped duplicate candidate: the pre-merge duplicate pass does not flag
// it with E0504.
func TestLoadRules_DuplicateRuleName_ComprehensionNotCandidate(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

if true {
	gen_a: {
		when: {x: 1}
		then: deny: {
			rule_id: "ga"
			reason:  "ga"
		}
	}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

if true {
	gen_b: {
		when: {x: 2}
		then: deny: {
			rule_id: "gb"
			reason:  "gb"
		}
	}
}
`)

	_, err := config.LoadRules(dir)
	for _, de := range collectDiags(err) {
		if de.D.Code == "E0504" {
			t.Fatalf("top-level comprehensions must not be flagged as duplicate rule names (E0504); got: %v", err)
		}
	}
}
