package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/evaluator"
)

// writeRuleFileNamed stages a fixture whose body is a full CUE file — it does
// not wrap the body in a synthetic `rule: { ... }` field, so callers can
// declare multiple named top-level rules (`allow_foo:`, `deny_bar:`) or mix
// hidden helpers (`_regex:`) into the fixture.
func writeRuleFileNamed(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestLoadRules_MultipleNamedRulesPerFile pins the new multi-rule-per-file
// contract: a single `.cue` file may declare several top-level rules under
// distinct field names and the loader returns them all.
func TestLoadRules_MultipleNamedRulesPerFile(t *testing.T) {
	const src = `package rules

allow_foo: {
	when: {hook_event_name: "PreToolUse", tool_name: "Read"}
	then: allow: true
}

deny_bar: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {
		rule_id: "bar"
		reason:  "blocked"
	}
}
`
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "combo.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules must accept multiple top-level rules per file, got: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	// Index by rule identity — ordering is pinned by a sibling test, here we
	// only care that both rules decoded with the right shape.
	var allow, deny *config.Rule
	for i := range rules {
		r := &rules[i]
		if r.Then == nil {
			t.Fatalf("rule %d has no Then action", i)
		}
		switch r.Then.Kind {
		case config.ActionAllow:
			allow = r
		case config.ActionDeny:
			deny = r
		}
	}
	if allow == nil {
		t.Fatal("expected a rule with Allow action (from allow_foo)")
	}
	if !allow.Then.Allow {
		t.Fatalf("expected allow.Then.Allow=true, got %v", allow.Then.Allow)
	}
	if deny == nil {
		t.Fatal("expected a rule with Deny action (from deny_bar)")
	}
	if deny.Then.RuleID != "bar" {
		t.Fatalf("expected deny rule_id=bar, got %q", deny.Then.RuleID)
	}
	if deny.Then.Reason != "blocked" {
		t.Fatalf("expected deny reason=blocked, got %q", deny.Then.Reason)
	}
}

// TestLoadRules_SourceEncodesFileAndFieldName pins the `Rule.Source` format
// after the refactor: `<rule-file-path>:<field-name>` so a matched rule can be
// traced back to both the file AND the specific named rule inside it.
func TestLoadRules_SourceEncodesFileAndFieldName(t *testing.T) {
	const src = `package rules

my_rule: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "x"
		reason:  "because"
	}
}
`
	dir := t.TempDir()
	p := writeRuleFileNamed(t, dir, "foo.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	want := filepath.ToSlash(p) + ":my_rule"
	if rules[0].Source != want {
		t.Fatalf("Rule.Source=%q, want %q", rules[0].Source, want)
	}
}

// TestLoadRules_DeclarationOrderPreservedWithinFile guards against alphabetic
// iteration of file fields. The fixture lists `alpha`, `charlie`, `beta` in
// that declaration order; the loader must emit rules in that same order.
func TestLoadRules_DeclarationOrderPreservedWithinFile(t *testing.T) {
	const src = `package rules

alpha: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-alpha"
		reason:  "a"
	}
}

charlie: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-charlie"
		reason:  "c"
	}
}

beta: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "r-beta"
		reason:  "b"
	}
}
`
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "ordered.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}

	want := []string{"r-alpha", "r-charlie", "r-beta"}
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

// TestLoadRules_HiddenHelperFieldIgnored pins the hidden-field escape hatch:
// a top-level field starting with `_` (CUE's hidden-field syntax) must be
// skipped by the loader AND must remain addressable from sibling rules as a
// local helper definition.
func TestLoadRules_HiddenHelperFieldIgnored(t *testing.T) {
	const src = `package rules

_regex: "^rm\\s+-rf"

danger: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: command: =~_regex
	}
	then: deny: {
		rule_id: "rm-rf"
		reason:  "rm -rf blocked"
	}
}
`
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "danger.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules must skip hidden fields and accept the rule: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected exactly 1 rule (hidden helper must not count), got %d", len(rules))
	}
	r := rules[0]
	if r.Then == nil || r.Then.RuleID != "rm-rf" {
		t.Fatalf("expected the `danger` rule to load, got %+v", r.Then)
	}

	// The `_regex` reference must resolve — if the loader compiled the file
	// without hidden-field access, the regex would be non-concrete and fail
	// to match `rm -rf /`.
	ctx := cuecontext.New()
	hit := ctx.CompileString(`{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: command: "rm -rf /"
	}`)
	if err := hit.Err(); err != nil {
		t.Fatalf("compile matching input: %v", err)
	}
	matches, _, err := evaluator.Evaluate([]config.Rule{r}, hit)
	if err != nil {
		t.Fatalf("evaluator.Evaluate: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected rule to match `rm -rf /` via _regex helper, got %d matches", len(matches))
	}

	miss := ctx.CompileString(`{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: command: "ls -la"
	}`)
	if err := miss.Err(); err != nil {
		t.Fatalf("compile non-matching input: %v", err)
	}
	matches, _, err = evaluator.Evaluate([]config.Rule{r}, miss)
	if err != nil {
		t.Fatalf("evaluator.Evaluate: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("_regex helper resolved incorrectly: `ls -la` should not match, got %d matches", len(matches))
	}
}

// TestLoadRules_NonRuleFieldErrorsWithFileAndField pins the load-error shape
// for a top-level non-hidden field that does not unify with `#Rule`. The
// error must name BOTH the file and the offending field so rule authors can
// locate the mistake immediately.
func TestLoadRules_NonRuleFieldErrorsWithFileAndField(t *testing.T) {
	const src = `package rules

helpers: {
	foo: "bar"
}

legit: {
	when: {hook_event_name: "PreToolUse"}
	then: deny: {
		rule_id: "ok"
		reason:  "fine"
	}
}
`
	dir := t.TempDir()
	p := writeRuleFileNamed(t, dir, "mixed.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected error for non-rule top-level field `helpers`, got nil")
	}
	msg := err.Error()
	base := filepath.Base(p)
	if !strings.Contains(msg, p) && !strings.Contains(msg, base) {
		t.Errorf("error should mention the rule file (%s or %s), got: %s", p, base, msg)
	}
	if !strings.Contains(msg, "helpers") {
		t.Errorf("error should name the offending field `helpers`, got: %s", msg)
	}
	// Unwrap check — CUE diagnostics must survive wrapping so callers can
	// render position metadata the same way the existing stdlib-error test
	// asserts.
	var cueErr cueerrors.Error
	if !errors.As(err, &cueErr) {
		t.Errorf("error should unwrap to cue/errors.Error, got type %T: %v", err, err)
	}
}

// TestLoadRules_MultiRuleWithStdlibImports composes the two features under
// test — multiple named rules per file AND stdlib sub-package imports —
// because the implementation must handle them together. Both rules must load
// with stdlib symbols fully resolved.
func TestLoadRules_MultiRuleWithStdlibImports(t *testing.T) {
	const src = `package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
	"github.com/srnnkls/fas/cue/path"
)

system_deny: {
	when: hook.#PreToolUse & tool.#Bash & path.#hasSystemTarget
	then: deny: {
		rule_id: "sys-path"
		reason:  "System path blocked"
	}
}

prompt_ask: {
	when: hook.#UserPromptSubmit
	then: ask: {
		rule_id:  "confirm-prompt"
		reason:   "confirm user prompt"
		question: "Proceed?"
	}
}
`
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "combo_stdlib.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules must handle multi-rule files with stdlib imports: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	var denyR, askR *config.Rule
	for i := range rules {
		r := &rules[i]
		if r.Then == nil {
			t.Fatalf("rule %d has no Then action", i)
		}
		switch r.Then.Kind {
		case config.ActionDeny:
			denyR = r
		case config.ActionAsk:
			askR = r
		}
	}
	if denyR == nil || denyR.Then.RuleID != "sys-path" {
		t.Fatalf("expected deny rule with rule_id=sys-path, got %+v", denyR)
	}
	if askR == nil || askR.Then.RuleID != "confirm-prompt" {
		t.Fatalf("expected ask rule with rule_id=confirm-prompt, got %+v", askR)
	}

	// Exercise the stdlib composites via the evaluator so resolution isn't
	// just structural: a real PreToolUse+Bash+system-target input must match
	// the deny rule, and a UserPromptSubmit input must match the ask rule.
	ctx := cuecontext.New()
	sysHit := ctx.CompileString(`{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: ["/etc/passwd"]
	}`)
	if err := sysHit.Err(); err != nil {
		t.Fatalf("compile system-path input: %v", err)
	}
	matches, _, err := evaluator.Evaluate([]config.Rule{*denyR}, sysHit)
	if err != nil {
		t.Fatalf("evaluator.Evaluate deny: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("deny rule should match PreToolUse+Bash+/etc/passwd, got %d matches", len(matches))
	}

	promptHit := ctx.CompileString(`{
		hook_event_name: "UserPromptSubmit"
		prompt:          "hello"
	}`)
	if err := promptHit.Err(); err != nil {
		t.Fatalf("compile prompt input: %v", err)
	}
	matches, _, err = evaluator.Evaluate([]config.Rule{*askR}, promptHit)
	if err != nil {
		t.Fatalf("evaluator.Evaluate ask: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("ask rule should match UserPromptSubmit input, got %d matches", len(matches))
	}

	// Source must encode field names so both rules are individually traceable.
	for _, r := range rules {
		if !strings.Contains(r.Source, ":") {
			t.Errorf("Rule.Source=%q must include `:<field-name>` suffix", r.Source)
		}
	}
}

// TestLoadRules_OnlyHiddenFields_ReturnsZeroRules pins the empty-rule-set
// contract for a file whose sole content is a hidden helper field. The loader
// must return zero rules without error — hidden fields never contribute to
// the rule set even when they are the only top-level declarations.
func TestLoadRules_OnlyHiddenFields_ReturnsZeroRules(t *testing.T) {
	const src = `package rules

_helper: 1
`
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "hidden_only.cue", src)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules must accept a file with only hidden fields, got: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules from a hidden-only file, got %d", len(rules))
	}
}
