package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/quae/internal/config"
)

// writeRuleFile is a small helper for staging fixture .cue files.
func writeRuleFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// A minimal deny rule: no severity provided, so the schema default
// ("HIGH") must apply.
const denyRuleSrc = `package rules

test_rule: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r1"
		reason:  "nope"
	}
}
`

func TestLoadRules_ValidDenyRule(t *testing.T) {
	dir := t.TempDir()
	writeRuleFile(t, dir, "deny.cue", denyRuleSrc)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules returned error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	r := rules[0]
	if r.Then == nil {
		t.Fatalf("expected rule.Then to be populated")
	}
	if r.Then.Kind != config.ActionDeny {
		t.Fatalf("expected Deny action, got %q", r.Then.Kind)
	}
	if r.Then.RuleID != "r1" {
		t.Fatalf("expected rule_id=r1, got %q", r.Then.RuleID)
	}
	if r.Then.Reason != "nope" {
		t.Fatalf("expected reason=nope, got %q", r.Then.Reason)
	}
	if r.Then.Severity != "HIGH" {
		t.Fatalf("expected default severity=HIGH, got %q", r.Then.Severity)
	}
}

func TestLoadRules_UnknownGateRejected(t *testing.T) {
	// `halt` is not a #Action member. The spec explicitly collapsed
	// #Halt/#Block into #Deny, so schema unification must fail.
	const src = `package rules

bad_halt: {
	when: {hook_event_name: "PreToolUse"}
	then: halt: {
		rule_id: "r1"
		reason:  "stop"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "bad.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatalf("expected error for unknown gate 'halt', got nil")
	}
	msg := err.Error()
	// The error should mention the offending field or the schema
	// constraint. Accept either hint so we don't over-specify CUE's
	// exact wording.
	if !strings.Contains(msg, "halt") && !strings.Contains(msg, "#Action") {
		t.Fatalf("error should reference 'halt' or '#Action', got: %s", msg)
	}
}

func TestLoadRules_MultipleRulesDeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	// Intentionally write in non-alphabetical order to prove sort is
	// by filename, not write order.
	writeRuleFile(t, dir, "c_third.cue", ruleWithID("r3"))
	writeRuleFile(t, dir, "a_first.cue", ruleWithID("r1"))
	writeRuleFile(t, dir, "b_second.cue", ruleWithID("r2"))

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}
	want := []string{"r1", "r2", "r3"}
	for i, r := range rules {
		if r.Then == nil {
			t.Fatalf("rule[%d].Then nil", i)
		}
		if r.Then.RuleID != want[i] {
			t.Fatalf("rule[%d]: expected rule_id=%s, got %s", i, want[i], r.Then.RuleID)
		}
	}
}

func TestLoadRules_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules on empty dir returned error: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestLoadRules_IgnoresNonCueFiles(t *testing.T) {
	dir := t.TempDir()
	writeRuleFile(t, dir, "README.md", "# notes\n")
	writeRuleFile(t, dir, "notes.txt", "ignored\n")
	writeRuleFile(t, dir, "valid.cue", ruleWithID("only"))

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 .cue rule, got %d", len(rules))
	}
	if rules[0].Then == nil || rules[0].Then.RuleID != "only" {
		t.Fatalf("expected the .cue file's rule to load, got %+v", rules[0])
	}
}

func TestLoadRules_ModifyAction(t *testing.T) {
	const src = `package rules

fix_command: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: modify: {
		rule_id: "r1"
		reason:  "fix it"
		updated_input: {command: "ls -la"}
		mode: "silent"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "modify.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	a := rules[0].Then
	if a == nil {
		t.Fatal("expected Then action")
	}
	if a.Kind != config.ActionModify {
		t.Fatalf("expected Modify, got %q", a.Kind)
	}
	if a.Mode != "silent" {
		t.Fatalf("expected mode=silent, got %q", a.Mode)
	}
	if a.Priority != 50 {
		t.Fatalf("expected default priority=50, got %d", a.Priority)
	}
	if a.UpdatedInput == nil {
		t.Fatalf("expected updated_input to be populated")
	}
	cmd, ok := a.UpdatedInput["command"].(string)
	if !ok || cmd != "ls -la" {
		t.Fatalf("expected updated_input.command=%q, got %v", "ls -la", a.UpdatedInput["command"])
	}
}

func TestLoadRules_InjectAction(t *testing.T) {
	const src = `package rules

note_prompt: {
	when: {hook_event_name: "UserPromptSubmit"}
	then: inject: {
		rule_id: "r1"
		text:    "note"
		channel: "agent"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "inject.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	a := rules[0].Then
	if a == nil {
		t.Fatal("expected Then action")
	}
	if a.Kind != config.ActionInject {
		t.Fatalf("expected Inject, got %q", a.Kind)
	}
	if a.Text != "note" {
		t.Fatalf("expected text=note, got %q", a.Text)
	}
	if a.Channel != "agent" {
		t.Fatalf("expected channel=agent, got %q", a.Channel)
	}
	if a.Priority != 50 {
		t.Fatalf("expected default priority=50, got %d", a.Priority)
	}
}

func TestLoadRules_MetaRequires(t *testing.T) {
	const src = `package rules

needs_signals: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r1"
		reason:  "needs signals"
	}
	meta: {
		requires: ["signal_foo", "signal_bar"]
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "meta.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Meta == nil {
		t.Fatal("expected Meta to be populated")
	}
	got := rules[0].Meta.Requires
	want := []string{"signal_foo", "signal_bar"}
	if len(got) != len(want) {
		t.Fatalf("expected %d requires, got %d (%v)", len(want), len(got), got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("requires[%d]=%q, want %q", i, got[i], name)
		}
	}
}

// TestLoadRules_WhenExposedAsCueValue pins the contract that Rule.When is a
// cue.Value ready for Unify by the evaluator. Concrete fields must be
// readable via LookupPath + String().
func TestLoadRules_WhenExposedAsCueValue(t *testing.T) {
	dir := t.TempDir()
	writeRuleFile(t, dir, "deny.cue", denyRuleSrc)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	when := rules[0].When
	if !when.Exists() {
		t.Fatalf("rule.When must expose a valid cue.Value")
	}

	hookEvent := when.LookupPath(cue.ParsePath("hook_event_name"))
	if err := hookEvent.Err(); err != nil {
		t.Fatalf("lookup hook_event_name: %v", err)
	}
	got, err := hookEvent.String()
	if err != nil {
		t.Fatalf("hook_event_name not a concrete string: %v", err)
	}
	if got != "PreToolUse" {
		t.Fatalf("expected hook_event_name=%q, got %q", "PreToolUse", got)
	}

	// WhenMap is the best-effort debug projection; for a fully concrete
	// `when` clause it should be populated.
	if rules[0].WhenMap == nil {
		t.Fatalf("expected WhenMap to be populated for a concrete when clause")
	}
	if rules[0].WhenMap["hook_event_name"] != "PreToolUse" {
		t.Fatalf("WhenMap.hook_event_name=%v, want %q",
			rules[0].WhenMap["hook_event_name"], "PreToolUse")
	}
}

// TestLoadRules_WhenAcceptsNonConcreteConstraints proves the loader no longer
// forces concreteness on `when`: a regex matcher is a legitimate constraint
// the evaluator resolves by unifying against the input. Previously this
// would error out inside decodeMap via v.Decode.
func TestLoadRules_WhenAcceptsNonConcreteConstraints(t *testing.T) {
	const src = `package rules

system_path_regex: {
	when: {
		hook_event_name: "PreToolUse"
		tool_input: command: =~"^(/etc|/sys)"
	}
	then: deny: {
		rule_id: "r1"
		reason:  "system path"
	}
}
`
	dir := t.TempDir()
	writeRuleFile(t, dir, "regex.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules must accept non-concrete when constraints: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	when := rules[0].When
	if !when.Exists() {
		t.Fatal("expected rule.When to exist")
	}

	// The regex constraint must survive unification: applying it to a
	// matching command passes, a non-matching command fails. Use a fresh
	// context — CUE's Unify/FillPath now accept values from different contexts.
	ctx := cuecontext.New()
	match := ctx.CompileString(`{
		hook_event_name: "PreToolUse"
		tool_input: command: "/etc/passwd"
	}`)
	if err := match.Err(); err != nil {
		t.Fatalf("compile matching input: %v", err)
	}
	if err := when.Unify(match).Validate(cue.Concrete(true)); err != nil {
		t.Fatalf("regex should match /etc/passwd, got %v", err)
	}

	miss := ctx.CompileString(`{
		hook_event_name: "PreToolUse"
		tool_input: command: "/home/user"
	}`)
	if err := miss.Err(); err != nil {
		t.Fatalf("compile non-matching input: %v", err)
	}
	if err := when.Unify(miss).Validate(cue.Concrete(true)); err == nil {
		t.Fatal("regex should reject /home/user, but unification succeeded")
	}
}

// ruleWithID constructs a minimal well-formed deny rule with a given rule_id,
// used by the ordering test.
func ruleWithID(id string) string {
	return `package rules

test_rule: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "` + id + `"
		reason:  "reason for ` + id + `"
	}
}
`
}
