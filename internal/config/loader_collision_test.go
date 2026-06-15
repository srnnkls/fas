package config_test

import (
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// findRuleByID locates a rule by decoded Then.RuleID, decoupling identity from the Source the caller then asserts.
func findRuleByID(t *testing.T, rules []config.Rule, ruleID string) config.Rule {
	t.Helper()
	for _, r := range rules {
		if r.Then != nil && r.Then.RuleID == ruleID {
			return r
		}
	}
	ids := make([]string, len(rules))
	for i, r := range rules {
		if r.Then != nil {
			ids[i] = r.Then.RuleID
		}
	}
	t.Fatalf("no rule with Then.RuleID %q; got rule_ids %v", ruleID, ids)
	return config.Rule{}
}

// CRP-013 — collision-proof overlay disambiguation. sanitizeVirtualRuleName
// rewrites `<stem>_test.cue` / `<stem>_tool.cue` to `<stem>_rule.cue` so CUE's
// build-tag filename filter does not drop them. That rewrite is NOT injective:
// a real `git_rule.cue` and a sanitized `git_test.cue` both want the virtual key
// `git_rule.cue`. The interim CRP-001 guard ERRORS on that clash. CRP-013 must
// instead disambiguate the overlay keys so BOTH files load — neither silently
// dropped, no spurious error — while keeping each rule's Source naming its OWN
// real file.

// TestLoadRules_VirtualNameCollision_TestVsRuleBothLoad: `git_test.cue` and
// `git_rule.cue` collide on virtual key `git_rule.cue`. Both must load, and
// each rule's Source must name its own real file. The cross-assertion (git_a
// belongs to git_test.cue, git_b to git_rule.cue) makes this sensitive: swap
// the owners and it fails.
//
// FAILS today: the interim guard returns
// "rule files ... map to the same overlay name" so LoadRules errors and no
// rules are returned.
func TestLoadRules_VirtualNameCollision_TestVsRuleBothLoad(t *testing.T) {
	dir := t.TempDir()
	testPath := writeRuleFileNamed(t, dir, "git_test.cue", `package rules

git_a: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "git-a"
		reason:  "from git_test.cue"
	}
}
`)
	rulePath := writeRuleFileNamed(t, dir, "git_rule.cue", `package rules

git_b: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "git-b"
		reason:  "from git_rule.cue"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("colliding virtual names must both load, got error: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules (git_a + git_b), got %d", len(rules))
	}

	a := findRuleByID(t, rules, "git-a")
	if a.Source != testPath+":git_a" {
		t.Errorf("git_a.Source = %q, want %q", a.Source, testPath+":git_a")
	}

	b := findRuleByID(t, rules, "git-b")
	if b.Source != rulePath+":git_b" {
		t.Errorf("git_b.Source = %q, want %q", b.Source, rulePath+":git_b")
	}
}

// TestLoadRules_VirtualNameCollision_ToolVsRuleBothLoad: the `_tool.cue` arm of
// the sanitizer. `foo_tool.cue` and `foo_rule.cue` both map to virtual
// `foo_rule.cue`; both must load with correct, distinct Sources.
//
// FAILS today: same interim collision guard.
func TestLoadRules_VirtualNameCollision_ToolVsRuleBothLoad(t *testing.T) {
	dir := t.TempDir()
	toolPath := writeRuleFileNamed(t, dir, "foo_tool.cue", `package rules

foo_x: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "foo-x"
		reason:  "from foo_tool.cue"
	}
}
`)
	rulePath := writeRuleFileNamed(t, dir, "foo_rule.cue", `package rules

foo_y: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "foo-y"
		reason:  "from foo_rule.cue"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("colliding _tool/_rule virtual names must both load, got error: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules (foo_x + foo_y), got %d", len(rules))
	}

	x := findRuleByID(t, rules, "foo-x")
	if x.Source != toolPath+":foo_x" {
		t.Errorf("foo_x.Source = %q, want %q", x.Source, toolPath+":foo_x")
	}

	y := findRuleByID(t, rules, "foo-y")
	if y.Source != rulePath+":foo_y" {
		t.Errorf("foo_y.Source = %q, want %q", y.Source, rulePath+":foo_y")
	}
}

// TestLoadRules_VirtualNameCollision_OrderPreservedThreeWay: disambiguation must
// not perturb the total rule order, which is file-alphabetical by REAL name,
// then declaration order within a file. The fixture mixes a colliding pair
// (a_rule.cue, a_test.cue — both sanitize to virtual a_rule.cue) with a
// non-colliding file (m.cue). Real-name alphabetical order is:
//
//	a_rule.cue  < a_test.cue  < m.cue
//
// so the expected rule_id sequence is unambiguous. a_rule.cue declares two
// rules to also pin within-file declaration order under disambiguation.
//
// FAILS today: the colliding pair trips the interim guard, so LoadRules errors.
func TestLoadRules_VirtualNameCollision_OrderPreservedThreeWay(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a_rule.cue", `package rules

ar_first: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "ar-first"
		reason:  "a_rule 1"
	}
}

ar_second: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "ar-second"
		reason:  "a_rule 2"
	}
}
`)
	writeRuleFileNamed(t, dir, "a_test.cue", `package rules

at_only: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "at-only"
		reason:  "a_test 1"
	}
}
`)
	writeRuleFileNamed(t, dir, "m.cue", `package rules

m_only: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "m-only"
		reason:  "m 1"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("mixed colliding + non-colliding dir must load, got error: %v", err)
	}

	want := []string{"ar-first", "ar-second", "at-only", "m-only"}
	if len(rules) != len(want) {
		t.Fatalf("expected %d rules, got %d", len(want), len(rules))
	}
	got := make([]string, len(rules))
	for i, r := range rules {
		if r.Then == nil {
			t.Fatalf("rules[%d].Then is nil", i)
		}
		got[i] = r.Then.RuleID
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rule[%d] rule_id=%q, want %q (full order got=%v want=%v)",
				i, got[i], want[i], got, want)
		}
	}
}

// TestLoadRules_VirtualNameCollision_MintedNameHitsRealFile pins the minted
// fallback against a real file literally named like the naive mint output.
// Files sort as b_rule.cue(0) < b_rule_2_rule.cue(1) < b_test.cue(2). b_test
// sanitizes to taken key b_rule.cue, so it mints; the naive minter produces
// `b_rule_2_rule.cue` — the natural key already claimed by the REAL
// b_rule_2_rule.cue at index 1 — silently overwriting one file's bytes. All
// three rules must load, each Source naming its own real file.
//
// FAILS against the naive (non-probing) minter: one rule is dropped because
// its overlay key was overwritten.
func TestLoadRules_VirtualNameCollision_MintedNameHitsRealFile(t *testing.T) {
	dir := t.TempDir()
	rulePath := writeRuleFileNamed(t, dir, "b_rule.cue", `package rules

b_one: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "b-one"
		reason:  "from b_rule.cue"
	}
}
`)
	collidePath := writeRuleFileNamed(t, dir, "b_rule_2_rule.cue", `package rules

b_two: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "b-two"
		reason:  "from b_rule_2_rule.cue"
	}
}
`)
	testPath := writeRuleFileNamed(t, dir, "b_test.cue", `package rules

b_three: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "b-three"
		reason:  "from b_test.cue"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("minted name colliding with a real file must still load all, got error: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules (b_one + b_two + b_three), got %d", len(rules))
	}

	one := findRuleByID(t, rules, "b-one")
	if one.Source != rulePath+":b_one" {
		t.Errorf("b_one.Source = %q, want %q", one.Source, rulePath+":b_one")
	}

	two := findRuleByID(t, rules, "b-two")
	if two.Source != collidePath+":b_two" {
		t.Errorf("b_two.Source = %q, want %q", two.Source, collidePath+":b_two")
	}

	three := findRuleByID(t, rules, "b-three")
	if three.Source != testPath+":b_three" {
		t.Errorf("b_three.Source = %q, want %q", three.Source, testPath+":b_three")
	}
}

// TestLoadRules_SanitizedName_TestFileKeepsAttribution guards that
// disambiguation did not break the original sanitize-for-build-tag behavior:
// a lone `_test.cue` file (virtual-rewritten to `_rule.cue`) still loads, and
// its rule's Source names the REAL `_test.cue` file — not the virtual name.
func TestLoadRules_SanitizedName_TestFileKeepsAttribution(t *testing.T) {
	dir := t.TempDir()
	testPath := writeRuleFileNamed(t, dir, "solo_test.cue", `package rules

solo_rule: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "solo-rule"
		reason:  "from solo_test.cue"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("sanitized _test.cue file must load, got error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	r := findRuleByID(t, rules, "solo-rule")
	if r.Source != testPath+":solo_rule" {
		t.Errorf("solo_rule.Source = %q, want %q (must name real _test.cue, not virtual _rule.cue)",
			r.Source, testPath+":solo_rule")
	}
}
