package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

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

func TestLoadRules_PackageClause_AllOmitted_LoadsAsImplicitPackage(t *testing.T) {
	dir := t.TempDir()
	aPath := writeRuleFileNamed(t, dir, "a.cue", `alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-alpha"
		reason:  "a"
	}
}
`)
	bPath := writeRuleFileNamed(t, dir, "b.cue", `beta: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-beta"
		reason:  "b"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("all-omitted clause must load as one implicit package, got: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules (alpha from a.cue, beta from b.cue), got %d", len(rules))
	}

	alpha := findRuleBySource(t, rules, filepath.Base(aPath)+":alpha")
	if alpha.Then == nil || alpha.Then.RuleID != "r-alpha" {
		t.Errorf("alpha rule from a.cue missing or wrong: %+v", alpha.Then)
	}
	beta := findRuleBySource(t, rules, filepath.Base(bPath)+":beta")
	if beta.Then == nil || beta.Then.RuleID != "r-beta" {
		t.Errorf("beta rule from b.cue missing or wrong: %+v", beta.Then)
	}
}

func TestLoadRules_PackageClause_MixedAbsentAndNamed_Loads(t *testing.T) {
	dir := t.TempDir()
	namedPath := writeRuleFileNamed(t, dir, "named.cue", `package rules

alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-alpha"
		reason:  "a"
	}
}
`)
	absentPath := writeRuleFileNamed(t, dir, "absent.cue", `beta: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-beta"
		reason:  "b"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("absent file must merge with the single `package rules` clause, got: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules (alpha + beta), got %d", len(rules))
	}

	alpha := findRuleBySource(t, rules, filepath.Base(namedPath)+":alpha")
	if alpha.Then == nil || alpha.Then.RuleID != "r-alpha" {
		t.Errorf("alpha rule from named.cue missing or wrong: %+v", alpha.Then)
	}
	beta := findRuleBySource(t, rules, filepath.Base(absentPath)+":beta")
	if beta.Then == nil || beta.Then.RuleID != "r-beta" {
		t.Errorf("beta rule from absent.cue missing or wrong: %+v", beta.Then)
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

func TestLoadRules_PackageClause_MixedAbsentAndSingleExplicit_MergesUnderCanonical(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "helper.cue", `_helper: "from absent file"

unused: {
	when: {hook_event_name: "PreToolUse"}
	then: allow: true
}
`)
	writeRuleFileNamed(t, dir, "consumer.cue", `package foo

consumer: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "consumer"
		reason:  _helper
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("absent file must adopt the single explicit `package foo` and merge, got: %v", err)
	}

	consumer := findRuleBySource(t, rules, "consumer.cue:consumer")
	if consumer.Then == nil {
		t.Fatalf("consumer rule has no Then action")
	}
	if consumer.Then.Reason != "from absent file" {
		t.Fatalf("cross-file `_helper` did not resolve across the merge: reason = %q, want %q",
			consumer.Then.Reason, "from absent file")
	}
}

// CUE's `package _` is the blank clause (PackageName()==""), counted as absent.
func TestLoadRules_PackageClause_BlankUnderscore_Loads(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "blank.cue", `package _

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
		t.Fatalf("`package _` (blank) must be treated as absent and load, got: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Then == nil || rules[0].Then.RuleID != "solo" {
		t.Fatalf("expected the `solo` rule, got %+v", rules[0].Then)
	}
}
