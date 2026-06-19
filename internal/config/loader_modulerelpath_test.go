package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// CRP-002 — ModuleRelPath. Each rule carries a NEW field, Rule.ModuleRelPath:
// the rule file's path RELATIVE to the directory passed to LoadRules (the
// synthetic module root). For a FLAT dir this is the bare filename; once subdir
// recursion lands (CRP-007) it becomes "<subdir>/<file>". Source is unchanged:
// it stays the full on-disk path "<dir>/<file>:<field>". ModuleRelPath is a
// SEPARATE carrier; it must NOT replace or alter Source, and must NOT contain
// the temp-dir prefix.
//
// RED today: Rule has no ModuleRelPath field, so this file does not compile.
// The compile failure IS the red signal; the assertions additionally pin the
// intended values so an empty/wrong field also fails once it exists.

func loadFlatModuleRelPathFixture(t *testing.T) (dir string, rules []config.Rule) {
	t.Helper()
	dir = t.TempDir()
	writeRuleFileNamed(t, dir, "a_bash.cue", `package rules

alpha: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {
		rule_id: "alpha"
		reason:  "from a_bash.cue"
	}
}
`)
	writeRuleFileNamed(t, dir, "sub_thing.cue", `package rules

beta: {
	when: {hook_event_name: "PreToolUse", tool_name: "Write"}
	then: deny: {
		rule_id: "beta"
		reason:  "from sub_thing.cue"
	}
}
`)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", dir, err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules (alpha + beta), got %d", len(rules))
	}
	return dir, rules
}

func TestLoadRules_ModuleRelPath_FlatDirIsBareFilename(t *testing.T) {
	dir, rules := loadFlatModuleRelPathFixture(t)

	want := map[string]string{
		"alpha": "a_bash.cue",
		"beta":  "sub_thing.cue",
	}
	for _, r := range rules {
		if r.Then == nil {
			t.Fatalf("rule has nil Then")
		}
		id := r.Then.RuleID
		w, ok := want[id]
		if !ok {
			t.Fatalf("unexpected rule_id %q", id)
		}
		if r.ModuleRelPath != w {
			t.Errorf("%s.ModuleRelPath = %q, want %q", id, r.ModuleRelPath, w)
		}
		if strings.Contains(r.ModuleRelPath, dir) {
			t.Errorf("%s.ModuleRelPath = %q must NOT contain the temp-dir prefix %q",
				id, r.ModuleRelPath, dir)
		}
	}
}

// Regression guard: adding ModuleRelPath must not alter Source.
func TestLoadRules_ModuleRelPath_SourceUnchanged(t *testing.T) {
	dir, rules := loadFlatModuleRelPathFixture(t)

	wantSource := map[string]string{
		"alpha": filepath.ToSlash(filepath.Join(dir, "a_bash.cue")) + ":alpha",
		"beta":  filepath.ToSlash(filepath.Join(dir, "sub_thing.cue")) + ":beta",
	}
	for _, r := range rules {
		id := r.Then.RuleID
		w := wantSource[id]
		if r.Source != w {
			t.Errorf("%s.Source = %q, want %q", id, r.Source, w)
		}
	}
}

func TestLoadRules_ModuleRelPath_DistinctFromSource(t *testing.T) {
	dir, rules := loadFlatModuleRelPathFixture(t)

	for _, r := range rules {
		id := r.Then.RuleID

		if r.Source == r.ModuleRelPath {
			t.Errorf("%s: Source and ModuleRelPath must differ, both = %q", id, r.Source)
		}
		if !strings.Contains(r.Source, filepath.ToSlash(dir)) {
			t.Errorf("%s.Source = %q, expected it to contain the on-disk dir %q", id, r.Source, dir)
		}
		// Source path component is "<dir>/<file>"; ModuleRelPath is "<file>".
		sourcePath := r.Source[:strings.LastIndex(r.Source, ":")]
		if filepath.Base(sourcePath) != r.ModuleRelPath {
			t.Errorf("%s: ModuleRelPath = %q, want basename of Source path %q (= %q)",
				id, r.ModuleRelPath, sourcePath, filepath.Base(sourcePath))
		}
	}
}

func TestLoadRules_ModuleRelPath_NoLeadingDotOrAbsolute(t *testing.T) {
	_, rules := loadFlatModuleRelPathFixture(t)

	for _, r := range rules {
		id := r.Then.RuleID
		if r.ModuleRelPath == "" {
			t.Errorf("%s.ModuleRelPath is empty", id)
			continue
		}
		if filepath.IsAbs(r.ModuleRelPath) {
			t.Errorf("%s.ModuleRelPath = %q must be relative, not absolute", id, r.ModuleRelPath)
		}
		if strings.HasPrefix(r.ModuleRelPath, "./") || strings.HasPrefix(r.ModuleRelPath, "../") {
			t.Errorf("%s.ModuleRelPath = %q must not have a leading ./ or ../ segment",
				id, r.ModuleRelPath)
		}
	}
}
