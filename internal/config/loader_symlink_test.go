package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
}

func loadIDs(t *testing.T, dir string, follow bool) ([]string, []string) {
	t.Helper()
	rules, skipped, err := config.LoadRulesWith(dir, config.LoadOptions{FollowSymlinks: follow})
	if err != nil {
		t.Fatalf("LoadRulesWith(%s, %v): %v", dir, follow, err)
	}
	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.Then.RuleID)
	}
	return ids, skipped
}

func TestLoadRules_SymlinkedRoot(t *testing.T) {
	target := t.TempDir()
	mkSubdirRuleFile(t, target, "root.cue", ruleBody("root_rule", "root_rule"))
	mkSubdirRuleFile(t, target, filepath.Join("sub", "sub.cue"), ruleBody("sub_rule", "sub_rule"))
	link := filepath.Join(t.TempDir(), "rules")
	symlink(t, target, link)

	ids, skipped := loadIDs(t, link, false)
	if len(ids) != 0 || !slices.Equal(skipped, []string{link}) {
		t.Fatalf("no-follow: ids=%v skipped=%v", ids, skipped)
	}
	ids, skipped = loadIDs(t, link, true)
	if !slices.Equal(ids, []string{"root_rule", "sub_rule"}) || len(skipped) != 0 {
		t.Fatalf("follow: ids=%v skipped=%v", ids, skipped)
	}
}

func TestLoadRules_SymlinkedSubdir(t *testing.T) {
	root := t.TempDir()
	shared := t.TempDir()
	mkSubdirRuleFile(t, root, "local.cue", ruleBody("local_rule", "local_rule"))
	mkSubdirRuleFile(t, shared, "shared.cue", ruleBody("shared_rule", "shared_rule"))
	symlink(t, shared, filepath.Join(root, "shared"))
	symlink(t, shared, filepath.Join(root, ".hidden"))

	ids, skipped := loadIDs(t, root, false)
	if !slices.Equal(ids, []string{"local_rule"}) || !slices.Equal(skipped, []string{filepath.Join(root, "shared")}) {
		t.Fatalf("no-follow: ids=%v skipped=%v", ids, skipped)
	}
	rules, _, err := config.LoadRulesWith(root, config.LoadOptions{FollowSymlinks: true})
	if err != nil {
		t.Fatal(err)
	}
	byID := rulesByID(rules)
	if len(rules) != 2 || byID["shared_rule"].ModuleRelPath != "shared/shared.cue" {
		t.Fatalf("follow: %+v", byID)
	}
}

func TestLoadRules_SymlinkCycle(t *testing.T) {
	root := t.TempDir()
	mkSubdirRuleFile(t, root, filepath.Join("a", "a.cue"), ruleBody("a_rule", "a_rule"))
	symlink(t, root, filepath.Join(root, "a", "loop"))

	ids, _ := loadIDs(t, root, true)
	if !slices.Equal(ids, []string{"a_rule"}) {
		t.Fatalf("ids=%v", ids)
	}
}
