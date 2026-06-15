package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// CRP-001 — merged-package load + per-file origin map + E0505 package-clause
// diagnostic. Policy AD-7: every .cue file in a rules dir must declare the
// same single explicit `package` clause, and the directory is loaded as ONE
// merged CUE package (not per-file isolation).

// findRuleBySource returns the loaded rule whose Source ends with the given
// "<file>:<field>" suffix, or fails. The suffix match keeps the assertion
// independent of the absolute temp-dir prefix while still pinning file+field.
func findRuleBySource(t *testing.T, rules []config.Rule, suffix string) config.Rule {
	t.Helper()
	for _, r := range rules {
		if strings.HasSuffix(r.Source, suffix) {
			return r
		}
	}
	got := make([]string, len(rules))
	for i, r := range rules {
		got[i] = r.Source
	}
	t.Fatalf("no rule with Source ending %q; got sources %v", suffix, got)
	return config.Rule{}
}

// TestLoadRules_MergedPackage_CrossFileSharedHelper pins the headline behavior:
// a hidden helper declared at the top level of file a.cue is visible to a rule
// in file b.cue because the directory loads as one merged package. The
// referencing rule's decoded deny reason must equal the helper's value.
//
// FAILS today: LoadRules compiles each file in isolation, so `_shared` is
// unbound inside b.cue and the load errors (or the reference never resolves).
func TestLoadRules_MergedPackage_CrossFileSharedHelper(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

_shared: "blocked by policy"
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

consumer: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "consumer"
		reason:  _shared
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("merged-package load must resolve cross-file `_shared`, got: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule (consumer), got %d", len(rules))
	}
	r := rules[0]
	if r.Then == nil {
		t.Fatalf("consumer rule has no Then action")
	}
	if r.Then.Reason != "blocked by policy" {
		t.Fatalf("cross-file `_shared` did not resolve: deny reason = %q, want %q",
			r.Then.Reason, "blocked by policy")
	}
}

// TestLoadRules_MergedPackage_SingleFileUnchanged guards that loading a
// directory as a merged package does not regress the degenerate one-file case:
// a single `package rules` file loads with the same rule and no error.
func TestLoadRules_MergedPackage_SingleFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "only.cue", `package rules

solo: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "solo"
		reason:  "lonely"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("single-file merged-package load: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Then == nil || rules[0].Then.RuleID != "solo" {
		t.Fatalf("expected the `solo` rule, got %+v", rules[0].Then)
	}
}

// TestLoadRules_MergedPackage_SourceNamesOriginFile pins the origin-map
// contract: under a merged package, each rule's Source must name ITS OWN
// originating file. rule `alpha` lives in a.cue, rule `beta` lives in b.cue;
// a naive merge that stamps every rule with one file path would fail this.
//
// The assertion is intentionally sensitive: swapping which file owns which
// rule must flip both expectations.
func TestLoadRules_MergedPackage_SourceNamesOriginFile(t *testing.T) {
	dir := t.TempDir()
	aPath := writeRuleFileNamed(t, dir, "a.cue", `package rules

alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-alpha"
		reason:  "a"
	}
}
`)
	bPath := writeRuleFileNamed(t, dir, "b.cue", `package rules

beta: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-beta"
		reason:  "b"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("merged-package load: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	alpha := findRuleBySource(t, rules, filepath.Base(aPath)+":alpha")
	if alpha.Source != aPath+":alpha" {
		t.Errorf("alpha.Source = %q, want %q", alpha.Source, aPath+":alpha")
	}

	beta := findRuleBySource(t, rules, filepath.Base(bPath)+":beta")
	if beta.Source != bPath+":beta" {
		t.Errorf("beta.Source = %q, want %q", beta.Source, bPath+":beta")
	}
}

// TestLoadRules_PackageClause_EmitsE0505_AllOmitted pins E0505 for the case
// where EVERY .cue file in the dir omits the `package` clause. Without an
// explicit, consistent clause AD-7 is violated and the load must error with a
// *diag.DiagError carrying code "E0505" that names the offending file(s).
//
// FAILS today: absent clauses load as an anonymous package, no error.
func TestLoadRules_PackageClause_EmitsE0505_AllOmitted(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-alpha"
		reason:  "a"
	}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `beta: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-beta"
		reason:  "b"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected E0505 when every file omits the package clause, got nil error")
	}
	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0505" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0505")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a.cue") || !strings.Contains(msg, "b.cue") {
		t.Errorf("E0505 should name BOTH offending files (a.cue AND b.cue); got: %s", msg)
	}
}

// TestLoadRules_PackageClause_EmitsE0505_MixedClauses pins E0505 for a dir
// where one file declares `package rules` and the other omits the clause. The
// offending (absent) file must be named.
func TestLoadRules_PackageClause_EmitsE0505_MixedClauses(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "good.cue", `package rules

alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-alpha"
		reason:  "a"
	}
}
`)
	writeRuleFileNamed(t, dir, "bad.cue", `beta: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-beta"
		reason:  "b"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected E0505 for a file missing the package clause, got nil error")
	}
	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0505" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0505")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bad.cue") {
		t.Errorf("E0505 should name the offending file `bad.cue`; got: %s", msg)
	}
	if strings.Contains(msg, "good.cue") {
		t.Errorf("E0505 must NOT name the canonical file `good.cue`; got: %s", msg)
	}
}

// TestLoadRules_PackageClause_EmitsE0505_DifferentNames pins E0505 for a dir
// whose files declare DIFFERENT explicit package names (`rules` vs `other`).
// AD-7 requires a single consistent clause, so the offending file is named.
func TestLoadRules_PackageClause_EmitsE0505_DifferentNames(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-alpha"
		reason:  "a"
	}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package other

beta: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-beta"
		reason:  "b"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected E0505 for divergent package names, got nil error")
	}
	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0505" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0505")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a.cue") || !strings.Contains(msg, "b.cue") {
		t.Errorf("E0505 should name BOTH conflicting files (a.cue AND b.cue); got: %s", msg)
	}
}

// TestLoadRules_PackageClause_ConsistentExplicit_LoadsNormally is the
// regression guard for the happy path: a dir whose files all declare the same
// explicit `package rules` clause loads with no error and returns both rules.
func TestLoadRules_PackageClause_ConsistentExplicit_LoadsNormally(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-alpha"
		reason:  "a"
	}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

beta: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-beta"
		reason:  "b"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("consistent explicit `package rules` clause must load cleanly, got: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}
