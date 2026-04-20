package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"

	"github.com/srnnkls/quae/internal/config"
	"github.com/srnnkls/quae/internal/evaluator"
)

// writeStdlibRuleFile stages a rule file and returns its absolute path so
// assertions can correlate errors with their source.
func writeStdlibRuleFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// compileInput turns a CUE source literal into a cue.Value suitable for
// unification against a rule's When clause.
func compileInput(t *testing.T, ctx *cue.Context, src string) cue.Value {
	t.Helper()
	v := ctx.CompileString(src, cue.Filename("input.cue"))
	if err := v.Err(); err != nil {
		t.Fatalf("compile input %q: %v", src, err)
	}
	return v
}

// ruleMatches routes through the public evaluator surface so the test does
// not reimplement match semantics. A non-empty match slice means the rule's
// when clause was satisfied by input.
func ruleMatches(t *testing.T, rule config.Rule, input cue.Value) bool {
	t.Helper()
	matches, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("evaluator.Evaluate: %v", err)
	}
	return len(matches) > 0
}

// TestLoadRules_RuleCanImportStdlibHasSystemTarget pins the core contract
// of this feature: a rule file can import the shipped stdlib and compose
// with `quae.#hasSystemTarget` instead of inlining the regex.
func TestLoadRules_RuleCanImportStdlibHasSystemTarget(t *testing.T) {
	src := `package rules

import "github.com/srnnkls/quae/cue:quae"

rule: {
	when: quae.#isPreToolUse & quae.#isBash & quae.#hasSystemTarget
	then: deny: {
		rule_id: "sys-path"
		reason:  "System path blocked"
	}
}
`
	dir := t.TempDir()
	writeStdlibRuleFile(t, dir, "system_path.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules must resolve the stdlib import, got: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	r := rules[0]
	if r.Then == nil {
		t.Fatal("expected Then action to be decoded")
	}
	if r.Then.RuleID != "sys-path" {
		t.Fatalf("expected rule_id=sys-path, got %q", r.Then.RuleID)
	}

	ctx := cuecontext.New()
	match := compileInput(t, ctx, `{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: ["/etc/passwd"]
	}`)
	if !ruleMatches(t, r, match) {
		t.Errorf("rule using quae.#hasSystemTarget should match targets=[/etc/passwd]")
	}

	miss := compileInput(t, ctx, `{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: ["./etc/passwd"]
	}`)
	if ruleMatches(t, r, miss) {
		t.Error("rule using quae.#hasSystemTarget must NOT match targets=[./etc/passwd] (relative, not a system prefix)")
	}
}

// TestLoadRules_RuleCanImportFlagConstraints covers the `cue/flags/rm.cue`
// slice of the stdlib so the feature doesn't regress to shipping only the
// root file.
func TestLoadRules_RuleCanImportFlagConstraints(t *testing.T) {
	src := `package rules

import "github.com/srnnkls/quae/cue:quae"

rule: {
	when: quae.#isPreToolUse & quae.#isBash & quae.#HasRmForce
	then: deny: {
		rule_id: "rm-force"
		reason:  "rm -f blocked"
	}
}
`
	dir := t.TempDir()
	writeStdlibRuleFile(t, dir, "rm_force.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules must resolve quae.#HasRmForce, got: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	r := rules[0]

	ctx := cuecontext.New()
	match := compileInput(t, ctx, `{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: flags: ["-rf"]
	}`)
	if !ruleMatches(t, r, match) {
		t.Error("rule using quae.#HasRmForce should match flags=[-rf]")
	}

	miss := compileInput(t, ctx, `{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: flags: ["-x"]
	}`)
	if ruleMatches(t, r, miss) {
		t.Error("rule using quae.#HasRmForce must NOT match flags=[-x]")
	}
}

// TestLoadRules_RuleWithoutStdlibImport_StillWorks is the backward-compat
// guard. Every existing policy in tests/policies/ inlines its constraints
// via `list.MatchN` — they must keep loading after the stdlib-import feature
// lands.
func TestLoadRules_RuleWithoutStdlibImport_StillWorks(t *testing.T) {
	src := `package rules

import "list"

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: list.MatchN(>0, =~"^/etc")
	}
	then: deny: {
		rule_id: "inline"
		reason:  "inline regex still works"
	}
}
`
	dir := t.TempDir()
	writeStdlibRuleFile(t, dir, "inline.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules must still accept rules without the stdlib import: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Then == nil || rules[0].Then.RuleID != "inline" {
		t.Fatalf("expected inline rule to load, got %+v", rules[0].Then)
	}

	ctx := cuecontext.New()
	match := compileInput(t, ctx, `{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: ["/etc/passwd"]
	}`)
	if !ruleMatches(t, rules[0], match) {
		t.Error("inline regex rule should still match /etc/passwd")
	}
}

// TestLoadRules_InvalidStdlibReference_ErrorsWithContext confirms the loader
// surfaces a useful diagnostic: the error must name the undefined symbol
// AND the rule file path so the author can locate the bad reference. It must
// also unwrap to a structured CUE diagnostic so callers can render positions.
func TestLoadRules_InvalidStdlibReference_ErrorsWithContext(t *testing.T) {
	src := `package rules

import "github.com/srnnkls/quae/cue:quae"

rule: {
	when: quae.#nonexistentDef
	then: deny: {
		rule_id: "bad"
		reason:  "references a symbol that is not in the stdlib"
	}
}
`
	dir := t.TempDir()
	path := writeStdlibRuleFile(t, dir, "bad_ref.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected error for reference to quae.#nonexistentDef, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "nonexistentDef") {
		t.Errorf("error should mention the undefined symbol 'nonexistentDef', got: %s", msg)
	}
	// Use the file basename — `filepath.Join` on absolute paths preserves
	// the full path, and CUE's diagnostics usually echo the filename with
	// it intact. Either the full path or the basename is acceptable.
	base := filepath.Base(path)
	if !strings.Contains(msg, path) && !strings.Contains(msg, base) {
		t.Errorf("error should mention the rule file path (%s) or basename (%s), got: %s", path, base, msg)
	}
	var cueErr cueerrors.Error
	if !errors.As(err, &cueErr) {
		t.Errorf("error should unwrap to cue/errors.Error for position metadata, got type %T: %v", err, err)
	}
}
