package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// CRP-007: each subdir containing .cue is an independent package; rules from
// all packages return together in CompareModulePath(ModuleRelPath) total order.

func mkSubdirRuleFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func ruleBody(ruleField, ruleID string) string {
	return "package rules\n\n" + ruleField + `: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {
		rule_id: "` + ruleID + `"
		reason:  "r"
	}
}
`
}

// rulesByID indexes loaded rules by their then.deny.rule_id for assertion.
func rulesByID(rules []config.Rule) map[string]config.Rule {
	out := make(map[string]config.Rule, len(rules))
	for _, r := range rules {
		if r.Then != nil {
			out[r.Then.RuleID] = r
		}
	}
	return out
}

func TestLoadRules_Subdirs_LoadAsSeparatePackages_TotalOrder_CRP007(t *testing.T) {
	root := t.TempDir()
	writeRuleFileNamed(t, root, "aa_flat.cue", ruleBody("flat_rule", "flat_rule"))
	mkSubdirRuleFile(t, root, filepath.Join("security", "git.cue"), ruleBody("sec_rule", "sec_rule"))
	mkSubdirRuleFile(t, root, filepath.Join("workflow", "deploy.cue"), ruleBody("wf_rule", "wf_rule"))

	rules, err := config.LoadRules(root)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", root, err)
	}

	wantIDs := []string{"flat_rule", "sec_rule", "wf_rule"}
	if len(rules) != len(wantIDs) {
		gotIDs := make([]string, len(rules))
		for i := range rules {
			if rules[i].Then != nil {
				gotIDs[i] = rules[i].Then.RuleID
			}
		}
		t.Fatalf("rule count = %d %v, want %d %v", len(rules), gotIDs, len(wantIDs), wantIDs)
	}
	for i, want := range wantIDs {
		if rules[i].Then == nil || rules[i].Then.RuleID != want {
			got := ""
			if rules[i].Then != nil {
				got = rules[i].Then.RuleID
			}
			t.Errorf("rules[%d].RuleID = %q, want %q", i, got, want)
		}
	}

	wantRel := map[string]string{
		"flat_rule": "aa_flat.cue",
		"sec_rule":  filepath.Join("security", "git.cue"),
		"wf_rule":   filepath.Join("workflow", "deploy.cue"),
	}
	for _, r := range rulesByID(rules) {
		if r.ModuleRelPath != wantRel[r.Then.RuleID] {
			t.Errorf("%s.ModuleRelPath = %q, want %q",
				r.Then.RuleID, r.ModuleRelPath, wantRel[r.Then.RuleID])
		}
	}
}

func TestLoadRules_Subdir_NestedModuleRelPathAndSource_CRP007(t *testing.T) {
	root := t.TempDir()
	writeRuleFileNamed(t, root, "aa_flat.cue", ruleBody("flat_rule", "flat_rule"))
	mkSubdirRuleFile(t, root, filepath.Join("security", "git.cue"), ruleBody("sec_rule", "sec_rule"))

	rules, err := config.LoadRules(root)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", root, err)
	}

	sec, ok := rulesByID(rules)["sec_rule"]
	if !ok {
		t.Fatalf("sec_rule not loaded from security/ subdir; got %d rules", len(rules))
	}

	wantRel := filepath.Join("security", "git.cue")
	if sec.ModuleRelPath != wantRel {
		t.Errorf("sec_rule.ModuleRelPath = %q, want %q", sec.ModuleRelPath, wantRel)
	}

	wantSource := filepath.ToSlash(filepath.Join(root, "security", "git.cue")) + ":sec_rule"
	if sec.Source != wantSource {
		t.Errorf("sec_rule.Source = %q, want %q", sec.Source, wantSource)
	}
	if filepath.IsAbs(sec.ModuleRelPath) {
		t.Errorf("sec_rule.ModuleRelPath = %q must be relative", sec.ModuleRelPath)
	}
}

func TestLoadRules_Subdir_SkipsEmptyDotUnderscoreAndNonCue_CRP007(t *testing.T) {
	root := t.TempDir()
	mkSubdirRuleFile(t, root, filepath.Join("security", "git.cue"), ruleBody("sec_rule", "sec_rule"))

	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not cue\n"), 0o600); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	mkSubdirRuleFile(t, root, filepath.Join(".hidden", "h.cue"), ruleBody("hidden_rule", "hidden_rule"))
	mkSubdirRuleFile(t, root, filepath.Join("_internal", "i.cue"), ruleBody("internal_rule", "internal_rule"))

	rules, err := config.LoadRules(root)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", root, err)
	}

	byID := rulesByID(rules)
	if _, ok := byID["sec_rule"]; !ok {
		t.Errorf("expected sec_rule from security/ to load; got %d rules", len(rules))
	}
	for _, bad := range []string{"hidden_rule", "internal_rule"} {
		if _, ok := byID[bad]; ok {
			t.Errorf("rule %q from a dotfile/underscore dir must NOT load", bad)
		}
	}
	if len(rules) != 1 {
		ids := make([]string, 0, len(rules))
		for id := range byID {
			ids = append(ids, id)
		}
		t.Errorf("expected exactly 1 rule (sec_rule), got %d: %v", len(rules), ids)
	}
}

// E0504 (duplicate rule name) is per-package, so the same name in two subdirs must not trip it.
func TestLoadRules_SameRuleNameDifferentSubdirs_Allowed_CRP007(t *testing.T) {
	root := t.TempDir()
	mkSubdirRuleFile(t, root, filepath.Join("security", "x.cue"), ruleBody("dup", "sec_dup"))
	mkSubdirRuleFile(t, root, filepath.Join("workflow", "y.cue"), ruleBody("dup", "wf_dup"))

	rules, err := config.LoadRules(root)
	if err != nil {
		t.Fatalf("same rule name in different subdirs must be allowed; got: %v", err)
	}

	byID := rulesByID(rules)
	for _, want := range []string{"sec_dup", "wf_dup"} {
		if _, ok := byID[want]; !ok {
			t.Errorf("expected rule_id %q to load from its own subdir-package; got %d rules", want, len(rules))
		}
	}
}

func TestLoadRules_SameRuleNameSameSubdir_EmitsE0504_CRP007(t *testing.T) {
	root := t.TempDir()
	mkSubdirRuleFile(t, root, filepath.Join("security", "x.cue"), ruleBody("dup", "x_dup"))
	mkSubdirRuleFile(t, root, filepath.Join("security", "y.cue"), ruleBody("dup", "y_dup"))

	_, err := config.LoadRules(root)
	if err == nil {
		t.Fatal("expected duplicate rule name within the same subdir to be rejected, got nil")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0504" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0504")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x.cue") {
		t.Errorf("E0504 error must name `x.cue`; got: %s", msg)
	}
	if !strings.Contains(msg, "y.cue") {
		t.Errorf("E0504 error must name `y.cue`; got: %s", msg)
	}
}

func TestLoadRules_FlatDirNoSubdirs_Unchanged_CRP007(t *testing.T) {
	root := t.TempDir()
	writeRuleFileNamed(t, root, "a.cue", ruleBody("flat_a", "flat_a"))
	writeRuleFileNamed(t, root, "b.cue", ruleBody("flat_b", "flat_b"))

	rules, err := config.LoadRules(root)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", root, err)
	}

	wantIDs := []string{"flat_a", "flat_b"}
	if len(rules) != len(wantIDs) {
		t.Fatalf("rule count = %d, want %d", len(rules), len(wantIDs))
	}
	for i, want := range wantIDs {
		if rules[i].Then == nil || rules[i].Then.RuleID != want {
			t.Errorf("rules[%d].RuleID mismatch, want %q", i, want)
		}
	}
	wantRel := map[string]string{"flat_a": "a.cue", "flat_b": "b.cue"}
	for _, r := range rulesByID(rules) {
		if r.ModuleRelPath != wantRel[r.Then.RuleID] {
			t.Errorf("%s.ModuleRelPath = %q, want %q",
				r.Then.RuleID, r.ModuleRelPath, wantRel[r.Then.RuleID])
		}
	}
}
