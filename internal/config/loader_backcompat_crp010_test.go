package config_test

import (
	"path/filepath"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/evaluator"
)

// CRP-010: flat-dir SEMANTIC equivalence after the merged-package/subdir
// refactor. A representative flat dir with multiple files and multiple rules
// per file must yield the exact (rule_id, Source, order) tuple set, and a known
// bad rule must still surface the same diagnostic CODE. This is the focused
// multi-rule companion to the CRP-015 baseline.

// TestBackcompat_FlatMultiRule_SourceOrderRuleIDTuples pins the full ordered
// (ModuleRelPath-order) tuple set for a flat dir whose files each declare
// several rules in non-alphabetical declaration order. Files sort lexically;
// within a file, declaration order is preserved.
func TestBackcompat_FlatMultiRule_SourceOrderRuleIDTuples(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a_first.cue", `package rules

zebra: {
	when: {tool_name: "Bash"}
	then: deny: {rule_id: "a-zebra", reason: "z"}
}
apple: {
	when: {tool_name: "Bash"}
	then: deny: {rule_id: "a-apple", reason: "a"}
}
`)
	writeRuleFileNamed(t, dir, "b_second.cue", `package rules

mango: {
	when: {tool_name: "Bash"}
	then: ask: {rule_id: "b-mango", reason: "m", question: "q?"}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", dir, err)
	}

	type tuple struct{ ruleID, source string }
	want := []tuple{
		{"a-zebra", filepath.ToSlash(dir) + "/a_first.cue:zebra"},
		{"a-apple", filepath.ToSlash(dir) + "/a_first.cue:apple"},
		{"b-mango", filepath.ToSlash(dir) + "/b_second.cue:mango"},
	}
	if len(rules) != len(want) {
		got := make([]string, len(rules))
		for i := range rules {
			if rules[i].Then != nil {
				got[i] = rules[i].Then.RuleID
			}
		}
		t.Fatalf("rule count = %d %v, want %d", len(rules), got, len(want))
	}
	for i, w := range want {
		if rules[i].Then == nil {
			t.Fatalf("rules[%d].Then is nil", i)
		}
		if rules[i].Then.RuleID != w.ruleID {
			t.Errorf("rules[%d].RuleID = %q, want %q", i, rules[i].Then.RuleID, w.ruleID)
		}
		if rules[i].Source != w.source {
			t.Errorf("rules[%d].Source = %q, want %q", i, rules[i].Source, w.source)
		}
	}
}

// TestBackcompat_FlatMultiRule_FiredOrderUnchanged pins the fired trace over the
// flat multi-rule corpus: matches follow source order, declaration order within
// a file is preserved, and a non-matching (Write-only) rule does not fire.
func TestBackcompat_FlatMultiRule_FiredOrderUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a_first.cue", `package rules

zebra: {
	when: {tool_name: "Bash"}
	then: deny: {rule_id: "a-zebra", reason: "z"}
}
apple: {
	when: {tool_name: "Bash"}
	then: deny: {rule_id: "a-apple", reason: "a"}
}
`)
	writeRuleFileNamed(t, dir, "b_second.cue", `package rules

write_only: {
	when: {tool_name: "Write"}
	then: deny: {rule_id: "b-write", reason: "w"}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", dir, err)
	}

	ctx := cuecontext.New()
	input := ctx.CompileString(`{hook_event_name: "PreToolUse", tool_name: "Bash"}`)
	if err := input.Err(); err != nil {
		t.Fatalf("compile input: %v", err)
	}

	matches, _, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	type fired struct{ ruleID, source string }
	want := []fired{
		{"a-zebra", filepath.ToSlash(dir) + "/a_first.cue:zebra"},
		{"a-apple", filepath.ToSlash(dir) + "/a_first.cue:apple"},
	}
	if len(matches) != len(want) {
		t.Fatalf("fired count = %d, want %d (matches=%+v)", len(matches), len(want), matches)
	}
	for i, w := range want {
		id := ""
		if matches[i].Action != nil {
			id = matches[i].Action.RuleID
		}
		if id != w.ruleID || matches[i].Rule.Source != w.source {
			t.Errorf("fired[%d] = (%q, %q), want (%q, %q)",
				i, id, matches[i].Rule.Source, w.ruleID, w.source)
		}
	}
}

// TestBackcompat_FlatBadRule_DiagnosticCodeUnchanged pins that a flat dir with a
// duplicate top-level rule label across files still surfaces the same E0504
// diagnostic CODE after the refactor.
func TestBackcompat_FlatBadRule_DiagnosticCodeUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

dup: {
	when: {tool_name: "Bash"}
	then: deny: {rule_id: "a", reason: "a"}
}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

dup: {
	when: {tool_name: "Bash"}
	then: deny: {rule_id: "b", reason: "b"}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected duplicate rule name in flat dir to be rejected, got nil")
	}
	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0504" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0504")
	}
}
