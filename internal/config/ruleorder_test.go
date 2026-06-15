package config_test

import (
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// CRP-004 — flat end-to-end order (guard). Subdir loading does not exist yet,
// so only the (filename, declaration-index) dimensions are exercisable through
// LoadRules. This guards the existing CRP-001 contract as a subsequence of the
// new total order. Likely GREEN today; it bites only if rules come back in the
// wrong (filename, decl) order.

func loadOrderFixture(t *testing.T) []config.Rule {
	t.Helper()
	dir := t.TempDir()

	// Files staged in non-alphabetical order; the loader must still emit them
	// filename-lexical (b_*.cue, then m_*.cue, then z_*.cue) regardless of
	// write order, with declaration order preserved within each file.
	writeRuleFileNamed(t, dir, "z_last.cue", `package rules

z1: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {rule_id: "z1", reason: "z"}
}

z2: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {rule_id: "z2", reason: "z"}
}
`)
	writeRuleFileNamed(t, dir, "b_first.cue", `package rules

b1: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {rule_id: "b1", reason: "b"}
}

b2: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {rule_id: "b2", reason: "b"}
}
`)
	writeRuleFileNamed(t, dir, "m_mid.cue", `package rules

m1: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {rule_id: "m1", reason: "m"}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	return rules
}

func ruleIDs(t *testing.T, rules []config.Rule) []string {
	t.Helper()
	ids := make([]string, len(rules))
	for i, r := range rules {
		if r.Then == nil {
			t.Fatalf("rules[%d].Then is nil", i)
		}
		ids[i] = r.Then.RuleID
	}
	return ids
}

func TestLoadRules_FlatTotalOrder(t *testing.T) {
	rules := loadOrderFixture(t)

	want := []string{"b1", "b2", "m1", "z1", "z2"}
	got := ruleIDs(t, rules)
	if len(got) != len(want) {
		t.Fatalf("rule count = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch: got=%v want=%v", got, want)
		}
	}
}

func TestLoadRules_OrderStableAcrossRepeatedLoads(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "z_last.cue", `package rules

z1: {when: {hook_event_name: "PreToolUse"}, then: deny: {rule_id: "z1", reason: "z"}}
z2: {when: {hook_event_name: "PreToolUse"}, then: deny: {rule_id: "z2", reason: "z"}}
`)
	writeRuleFileNamed(t, dir, "b_first.cue", `package rules

b1: {when: {hook_event_name: "PreToolUse"}, then: deny: {rule_id: "b1", reason: "b"}}
b2: {when: {hook_event_name: "PreToolUse"}, then: deny: {rule_id: "b2", reason: "b"}}
`)

	var first []string
	for i := range 3 {
		rules, err := config.LoadRules(dir)
		if err != nil {
			t.Fatalf("LoadRules (iter %d): %v", i, err)
		}
		ids := ruleIDs(t, rules)
		if i == 0 {
			first = ids
			continue
		}
		if len(ids) != len(first) {
			t.Fatalf("iter %d: rule count = %d, want %d", i, len(ids), len(first))
		}
		for j := range first {
			if ids[j] != first[j] {
				t.Fatalf("iter %d order differs: got=%v first=%v", i, ids, first)
			}
		}
	}
}
