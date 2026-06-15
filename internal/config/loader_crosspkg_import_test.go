package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

func TestLoadRules_CrossPackageDefImport_Resolves(t *testing.T) {
	root := t.TempDir()

	mkSubdirRuleFile(t, root, "schema/defs.cue", `package schema

#Base: {
	hook_event_name: "PreToolUse"
	tool_name:       string
}
`)
	mkSubdirRuleFile(t, root, "security/git.cue", `package rules

import "fas.local/rules/schema"

bash_guard: {
	when: schema.#Base & {tool_name: "Bash"}
	then: deny: {
		rule_id: "bash-guard"
		reason:  "guarded via shared schema.#Base"
	}
}
`)

	rules, err := config.LoadRules(root)
	if err != nil {
		t.Fatalf("LoadRules must resolve cross-package import of fas.local/rules/schema, got: %v", err)
	}

	byID := rulesByID(rules)
	r, ok := byID["bash-guard"]
	if !ok {
		ids := make([]string, 0, len(byID))
		for id := range byID {
			ids = append(ids, id)
		}
		t.Fatalf("expected rule_id \"bash-guard\" composed from schema.#Base; got %d rules: %v", len(rules), ids)
	}
	if r.Then == nil || r.Then.Kind != config.ActionDeny {
		t.Fatalf("expected bash-guard to decode a deny action, got %+v", r.Then)
	}
}

// Root and a subdir both declare `package rules` and both define a rule named
// `shared`. Same rule NAME across different packages is legal (CRP-007), so the
// load must SUCCEED and return both rules isolated — not fold the root's
// same-named field into the subdir instance.
func TestLoadRules_RootAndSubdir_SamePackageName_NoCrossFold(t *testing.T) {
	root := t.TempDir()

	mkSubdirRuleFile(t, root, "root.cue", `package rules

shared: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {
		rule_id: "root_shared"
		reason:  "r"
	}
}
`)
	mkSubdirRuleFile(t, root, "security/sec.cue", `package rules

shared: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {
		rule_id: "sec_shared"
		reason:  "s"
	}
}
`)

	rules, err := config.LoadRules(root)
	if err != nil {
		t.Fatalf("root and subdir sharing a package name must load isolated, got: %v", err)
	}

	byID := rulesByID(rules)
	rootRule, ok := byID["root_shared"]
	if !ok {
		t.Fatalf("expected root_shared rule; got %d rules: %v", len(rules), byID)
	}
	if rootRule.ModuleRelPath != "root.cue" {
		t.Errorf("root_shared ModuleRelPath = %q, want %q", rootRule.ModuleRelPath, "root.cue")
	}

	secRule, ok := byID["sec_shared"]
	if !ok {
		t.Fatalf("expected sec_shared rule; got %d rules: %v", len(rules), byID)
	}
	if want := filepath.Join("security", "sec.cue"); secRule.ModuleRelPath != want {
		t.Errorf("sec_shared ModuleRelPath = %q, want %q", secRule.ModuleRelPath, want)
	}
}

// CUE folds an ancestor-directory package's same-clause-named fields into a
// descendant instance (CRP-008); nested `a/` and `a/b/` both `package rules`
// with a `shared` rule must load isolated, not collide.
func TestLoadRules_NestedParentChild_SamePackageName_NoCrossFold(t *testing.T) {
	root := t.TempDir()

	mkSubdirRuleFile(t, root, "a/x.cue", `package rules

shared: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {
		rule_id: "a_shared"
		reason:  "a"
	}
}
`)
	mkSubdirRuleFile(t, root, "a/b/y.cue", `package rules

shared: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {
		rule_id: "ab_shared"
		reason:  "b"
	}
}
`)

	rules, err := config.LoadRules(root)
	if err != nil {
		t.Fatalf("nested parent/child sharing a package name must load isolated, got: %v", err)
	}

	byID := rulesByID(rules)
	parentRule, ok := byID["a_shared"]
	if !ok {
		t.Fatalf("expected a_shared rule; got %d rules: %v", len(rules), byID)
	}
	if want := filepath.Join("a", "x.cue"); parentRule.ModuleRelPath != want {
		t.Errorf("a_shared ModuleRelPath = %q, want %q", parentRule.ModuleRelPath, want)
	}

	childRule, ok := byID["ab_shared"]
	if !ok {
		t.Fatalf("expected ab_shared rule; got %d rules: %v", len(rules), byID)
	}
	if want := filepath.Join("a", "b", "y.cue"); childRule.ModuleRelPath != want {
		t.Errorf("ab_shared ModuleRelPath = %q, want %q", childRule.ModuleRelPath, want)
	}
}

func TestLoadRules_ImportMissingRulesSubpackage_ClearError(t *testing.T) {
	root := t.TempDir()

	mkSubdirRuleFile(t, root, "security/git.cue", `package rules

import "fas.local/rules/nope"

bash_guard: {
	when: nope.#Base & {tool_name: "Bash"}
	then: deny: {
		rule_id: "bash-guard"
		reason:  "imports a package that does not exist"
	}
}
`)

	_, err := config.LoadRules(root)
	if err == nil {
		t.Fatal("expected a load error for import of nonexistent fas.local/rules/nope, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "nope") {
		t.Errorf("error should reference the missing import path (\"nope\"); got: %s", msg)
	}
}

func TestLoadRules_HiddenFieldNotVisibleAcrossImport(t *testing.T) {
	root := t.TempDir()

	mkSubdirRuleFile(t, root, "schema/defs.cue", `package schema

_secret: "x"

#Base: {
	hook_event_name: "PreToolUse"
	tool_name:       string
}
`)
	mkSubdirRuleFile(t, root, "security/git.cue", `package rules

import "fas.local/rules/schema"

bash_guard: {
	when: schema.#Base & {tool_name: schema._secret}
	then: deny: {
		rule_id: "bash-guard"
		reason:  "tries to read a hidden field across the import boundary"
	}
}
`)

	_, err := config.LoadRules(root)
	if err == nil {
		t.Fatal("expected load to fail: a hidden field (_secret) must not be visible across an import boundary, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "_secret") && !strings.Contains(msg, "secret") {
		t.Errorf("error should attribute the failure to the inaccessible hidden field _secret; got: %s", msg)
	}
}

// RED now: duplicateRuleNameError renders only bare basenames (filepath.Base),
// so security/dup.cue is indistinguishable from a same-basename file in another
// subdir; this asserts the intended subdir-qualified attribution (CRP-007-M1).
func TestLoadRules_SubdirQualifiedE0504Attribution(t *testing.T) {
	root := t.TempDir()

	mkSubdirRuleFile(t, root, "security/dup.cue", ruleBody("clash", "sec_clash_a"))
	mkSubdirRuleFile(t, root, "security/other.cue", ruleBody("clash", "sec_clash_b"))

	mkSubdirRuleFile(t, root, "workflow/dup.cue", ruleBody("wf_rule", "wf_rule"))

	_, err := config.LoadRules(root)
	if err == nil {
		t.Fatal("expected E0504 for duplicate rule name within security/, got nil")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError; got: %v", err)
	}
	if de.D.Code != "E0504" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0504")
	}

	msg := err.Error()
	if !strings.Contains(msg, "security/dup.cue") && !strings.Contains(msg, "security"+string('/')+"dup.cue") {
		t.Errorf("E0504 error must identify the offending file subdir-qualified (e.g. security/dup.cue), not just the bare basename; got: %s", msg)
	}
}
