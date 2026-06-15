package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// Defect A — a rule under a QUOTED top-level label must load. CUE addresses a
// quoted string label `"dash-rule"` by its unquoted name `dash-rule`; it is a
// regular (non-hidden) field and must therefore contribute a rule. Today the
// loader silently drops it (returns 0 rules, no error).
func TestLoadRules_QuotedRuleLabel_Loads(t *testing.T) {
	dir := t.TempDir()
	p := writeRuleFileNamed(t, dir, "dash.cue", `package rules

"dash-rule": {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-dash"
		reason:  "dashed"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("quoted-label rule must load without error, got: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected exactly 1 rule from a quoted top-level label, got %d", len(rules))
	}

	r := findRuleBySource(t, rules, filepath.Base(p)+":dash-rule")
	if r.Source != p+":dash-rule" {
		t.Errorf("Rule.Source = %q, want %q", r.Source, p+":dash-rule")
	}
	if r.Then == nil {
		t.Fatalf("quoted-label rule decoded with nil Then action")
	}
	if r.Then.Kind != config.ActionDeny {
		t.Errorf("Then.Kind = %q, want %q", r.Then.Kind, config.ActionDeny)
	}
	if r.Then.RuleID != "r-dash" {
		t.Errorf("Then.RuleID = %q, want %q", r.Then.RuleID, "r-dash")
	}
	if r.Then.Reason != "dashed" {
		t.Errorf("Then.Reason = %q, want %q", r.Then.Reason, "dashed")
	}
}

// TestLoadRules_QuotedAndIdentRules_BothLoad pins that a quoted label and an
// ident label in the same file both load, independent of declaration order.
// A quoted label must not break sibling ident rules.
func TestLoadRules_QuotedAndIdentRules_BothLoad(t *testing.T) {
	dir := t.TempDir()
	p := writeRuleFileNamed(t, dir, "mix.cue", `package rules

plain: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-plain"
		reason:  "plain"
	}
}

"dash-rule": {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-dash"
		reason:  "dashed"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("mixed quoted/ident rules must load without error, got: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules (plain + dash-rule), got %d", len(rules))
	}

	plain := findRuleBySource(t, rules, filepath.Base(p)+":plain")
	if plain.Source != p+":plain" {
		t.Errorf("plain.Source = %q, want %q", plain.Source, p+":plain")
	}
	if plain.Then == nil || plain.Then.RuleID != "r-plain" {
		t.Errorf("expected ident rule `plain` with rule_id r-plain, got %+v", plain.Then)
	}

	dash := findRuleBySource(t, rules, filepath.Base(p)+":dash-rule")
	if dash.Source != p+":dash-rule" {
		t.Errorf("dash.Source = %q, want %q", dash.Source, p+":dash-rule")
	}
	if dash.Then == nil || dash.Then.RuleID != "r-dash" {
		t.Errorf("expected quoted rule `dash-rule` with rule_id r-dash, got %+v", dash.Then)
	}
}

// Defect C1 — two files declaring the SAME top-level rule name with COMPATIBLE
// (identical) bodies must FAIL LOUDLY rather than silently merge into one rule.
// The bodies are identical so CUE unifies them cleanly and does NOT raise its
// own conflict error; we are pinning the silent compatible-duplicate case. The
// interim guard names BOTH files — no specific E05xx code asserted.
func TestLoadRules_DuplicateRuleNameAcrossFiles_FailsLoudly(t *testing.T) {
	const body = `package rules

alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-alpha"
		reason:  "a"
	}
}
`
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", body)
	writeRuleFileNamed(t, dir, "b.cue", body)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected a loud error when the same rule name is declared in two files, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a.cue") {
		t.Errorf("duplicate-rule error must name `a.cue`; got: %s", msg)
	}
	if !strings.Contains(msg, "b.cue") {
		t.Errorf("duplicate-rule error must name `b.cue`; got: %s", msg)
	}
}

// Defect C1b — two files declaring the SAME top-level rule name with
// INCOMPATIBLE bodies must hit the duplicate-name guard (naming both files)
// rather than CUE's generic merge-conflict error. The pre-merge AST guard must
// pre-empt compilation so the diagnostic is consistent with the compatible case.
func TestLoadRules_IncompatibleDuplicateRuleName_NamesBothFiles(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

alpha: {
	when: {x: 1}
	then: deny: {
		rule_id: "a"
		reason:  "a"
	}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

alpha: {
	when: {x: 2}
	then: deny: {
		rule_id: "b"
		reason:  "b"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected a loud error when the same rule name is declared incompatibly in two files, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "declared in both") {
		t.Errorf("incompatible duplicate must hit the duplicate-name guard, not a raw CUE conflict; got: %s", msg)
	}
	if !strings.Contains(msg, "a.cue") {
		t.Errorf("duplicate-rule error must name `a.cue`; got: %s", msg)
	}
	if !strings.Contains(msg, "b.cue") {
		t.Errorf("duplicate-rule error must name `b.cue`; got: %s", msg)
	}
}

// Defect C2 (legality guard) — sharing a hidden helper `_shared` and a `#Def`
// declared identically across files is LEGAL CUE package merging and must NOT
// be flagged by the duplicate-rule guard. Each file owns a DISTINCT rule. This
// guards that the Fix-C duplicate detection exempts hidden/def fields. Expected
// GREEN now and after the fix.
func TestLoadRules_SharedHelperAcrossFiles_NoError(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

_shared: "blocked"

#Common: {hook_event_name: "PreToolUse"}

alpha: {
	when: #Common
	then: deny: {
		rule_id: "r-alpha"
		reason:  _shared
	}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

_shared: "blocked"

#Common: {hook_event_name: "PreToolUse"}

beta: {
	when: #Common
	then: deny: {
		rule_id: "r-beta"
		reason:  _shared
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("shared hidden helper / #Def across files is legal and must not error, got: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 distinct rules (alpha + beta), got %d", len(rules))
	}
}
