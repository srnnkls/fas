package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CRP-009 — these guard --explain path attribution for subdir/nested rule
// packages: a revert of CRP-008's load.FromFile overlay (real on-disk paths)
// or a break in ruleIDForDiag's full-path match would trip them.

// writeNestedRuleFiles is writeRuleFiles with intermediate-directory
// creation so subdir-package keys ("security/git.cue") resolve.
func writeNestedRuleFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return dir
}

// missRuleFor builds a rule keyed on a tool_input.<missKey> a plain Bash
// payload never carries, so evaluation yields an E0201 miss anchored on the
// missKey line and attributed to ruleID.
func missRuleFor(pkg, ruleField, ruleID, missKey string) string {
	return "package " + pkg + "\n\n" +
		ruleField + ": {\n" +
		"\twhen: {\n" +
		"\t\thook_event_name: \"PreToolUse\"\n" +
		"\t\ttool_name:       \"Bash\"\n" +
		"\t\ttool_input: {\n" +
		"\t\t\t" + missKey + ": present: true\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\tthen: deny: {\n" +
		"\t\trule_id:  \"" + ruleID + "\"\n" +
		"\t\treason:   \"miss\"\n" +
		"\t\tseverity: \"HIGH\"\n" +
		"\t}\n" +
		"}\n"
}

// ruleIDBlocks splits explain stderr per `rule_id:` header so a source line
// can be asserted INSIDE its own rule's block — a cross-attribution bug that
// merely puts both lines somewhere in stderr would otherwise pass.
func ruleIDBlocks(stderr string) map[string]string {
	blocks := map[string]string{}
	const marker = "rule_id: "
	idx := strings.Index(stderr, marker)
	for idx >= 0 {
		rest := stderr[idx+len(marker):]
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		id := strings.TrimSpace(rest[:nl])
		body := rest[nl+1:]
		next := strings.Index(body, marker)
		if next >= 0 {
			blocks[id] = body[:next]
			idx = idx + len(marker) + nl + 1 + next
		} else {
			blocks[id] = body
			break
		}
	}
	return blocks
}

func TestRun_Explain_NestedRuleAttributesAndRendersSource(t *testing.T) {
	projectDir := writeNestedRuleFiles(t, map[string]string{
		"security/git.cue": missRuleFor("security", "sec_git_rule", "sec_git", "secflag"),
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, claudeBashInput("ls"),
		"eval", "--harness", "claude",
		"--config", projectDir, "--global-config", globalDir,
		"--explain=missed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	stderr := string(res.stderr)
	if !strings.Contains(stderr, "rule_id: sec_git") {
		t.Errorf("nested rule diagnostic must attribute rule_id `sec_git`; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, filepath.Join("security", "git.cue")) {
		t.Errorf("diagnostic must reference the subdir file path security/git.cue; stderr=%q", stderr)
	}
	// The offending source line is rendered into the frame: primeFileCache
	// must have read security/git.cue under the spelling the diagnostic
	// position carries, or the renderer would degrade to `position unknown`.
	if !strings.Contains(stderr, "secflag: present: true") {
		t.Errorf("diagnostic must render the offending source line `secflag: present: true`; stderr=%q", stderr)
	}
	if strings.Contains(stderr, "position unknown") {
		t.Errorf("nested rule diagnostic degraded to `position unknown`; stderr=%q", stderr)
	}
}

// Two files named git.cue in different subdirs: ruleIDForDiag's full-path
// match must disambiguate, or both attribute to whichever the basename
// fallback hits first.
func TestRun_Explain_SameBasenameSubdirsDoNotCrossAttribute(t *testing.T) {
	projectDir := writeNestedRuleFiles(t, map[string]string{
		"security/git.cue": missRuleFor("security", "sec_git_rule", "sec_git", "secflag"),
		"net/git.cue":      missRuleFor("net", "net_git_rule", "net_git", "netflag"),
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, claudeBashInput("ls"),
		"eval", "--harness", "claude",
		"--config", projectDir, "--global-config", globalDir,
		"--explain=missed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	stderr := string(res.stderr)
	blocks := ruleIDBlocks(stderr)

	sec, ok := blocks["sec_git"]
	if !ok {
		t.Fatalf("expected a `rule_id: sec_git` block; stderr=%q", stderr)
	}
	net, ok := blocks["net_git"]
	if !ok {
		t.Fatalf("expected a `rule_id: net_git` block; stderr=%q", stderr)
	}
	// Each rule_id's own block must carry its own source line and path, and
	// must NOT carry the other rule's source line.
	if !strings.Contains(sec, "secflag: present: true") ||
		!strings.Contains(sec, filepath.Join("security", "git.cue")) {
		t.Errorf("sec_git block must render security/git.cue source line; block=%q", sec)
	}
	if strings.Contains(sec, "netflag: present: true") {
		t.Errorf("sec_git block must NOT carry net/git.cue source line (cross-attribution); block=%q", sec)
	}
	if !strings.Contains(net, "netflag: present: true") ||
		!strings.Contains(net, filepath.Join("net", "git.cue")) {
		t.Errorf("net_git block must render net/git.cue source line; block=%q", net)
	}
	if strings.Contains(net, "secflag: present: true") {
		t.Errorf("net_git block must NOT carry security/git.cue source line (cross-attribution); block=%q", net)
	}
}

// sanitizeVirtualRuleName rewrites the overlay key for `_test.cue`, but the
// diagnostic must still resolve to the real on-disk policy_test.cue path.
func TestRun_Explain_TestSuffixRuleKeepsAttribution(t *testing.T) {
	projectDir := writeNestedRuleFiles(t, map[string]string{
		"policy_test.cue": missRuleFor("rules", "policy_rule", "policy", "polflag"),
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, claudeBashInput("ls"),
		"eval", "--harness", "claude",
		"--config", projectDir, "--global-config", globalDir,
		"--explain=missed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	stderr := string(res.stderr)
	if !strings.Contains(stderr, "rule_id: policy") {
		t.Errorf("_test.cue rule must attribute rule_id `policy`; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "policy_test.cue") {
		t.Errorf("diagnostic must reference the real on-disk file policy_test.cue; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "polflag: present: true") {
		t.Errorf("_test.cue rule diagnostic must render the offending source line; stderr=%q", stderr)
	}
	if strings.Contains(stderr, "position unknown") {
		t.Errorf("_test.cue rule diagnostic degraded to `position unknown`; stderr=%q", stderr)
	}
}

// CRP-013: git_test.cue and git_rule.cue sanitize to the same virtual name;
// collision-proof keying must keep each diagnostic resolvable to its real path.
func TestRun_Explain_CollisionDisambiguatedPairKeepsAttribution(t *testing.T) {
	projectDir := writeNestedRuleFiles(t, map[string]string{
		"git_test.cue": missRuleFor("rules", "git_test_rule", "git_test_id", "aaaflag"),
		"git_rule.cue": missRuleFor("rules", "git_rule_rule", "git_rule_id", "bbbflag"),
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, claudeBashInput("ls"),
		"eval", "--harness", "claude",
		"--config", projectDir, "--global-config", globalDir,
		"--explain=missed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	stderr := string(res.stderr)
	blocks := ruleIDBlocks(stderr)

	testBlock, ok := blocks["git_test_id"]
	if !ok {
		t.Fatalf("expected a `rule_id: git_test_id` block; stderr=%q", stderr)
	}
	ruleBlock, ok := blocks["git_rule_id"]
	if !ok {
		t.Fatalf("expected a `rule_id: git_rule_id` block; stderr=%q", stderr)
	}
	if !strings.Contains(testBlock, "aaaflag: present: true") ||
		!strings.Contains(testBlock, "git_test.cue") {
		t.Errorf("git_test_id block must render git_test.cue source line; block=%q", testBlock)
	}
	if strings.Contains(testBlock, "bbbflag: present: true") {
		t.Errorf("git_test_id block must NOT carry git_rule.cue source line; block=%q", testBlock)
	}
	if !strings.Contains(ruleBlock, "bbbflag: present: true") ||
		!strings.Contains(ruleBlock, "git_rule.cue") {
		t.Errorf("git_rule_id block must render git_rule.cue source line; block=%q", ruleBlock)
	}
	if strings.Contains(ruleBlock, "aaaflag: present: true") {
		t.Errorf("git_rule_id block must NOT carry git_test.cue source line; block=%q", ruleBlock)
	}
}
