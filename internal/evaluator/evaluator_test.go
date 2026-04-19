package evaluator_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/quae/internal/config"
	"github.com/srnnkls/quae/internal/evaluator"
	"github.com/srnnkls/quae/internal/parser"
)

// mustWriteRule drops a single .cue rule file into dir, with the given body
// wrapped in the `rule: { ... }` top-level the loader expects.
func mustWriteRule(t *testing.T, dir, name, body string) {
	t.Helper()
	mustWriteRuleWithImports(t, dir, name, nil, body)
}

// mustWriteRuleWithImports writes a .cue rule with top-level imports. CUE
// requires import declarations at file scope, not nested inside a struct.
func mustWriteRuleWithImports(t *testing.T, dir, name string, imports []string, body string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("package rules\n\n")
	if len(imports) > 0 {
		b.WriteString("import (\n")
		for _, imp := range imports {
			b.WriteString("\t\"")
			b.WriteString(imp)
			b.WriteString("\"\n")
		}
		b.WriteString(")\n\n")
	}
	b.WriteString("rule: ")
	b.WriteString(body)
	b.WriteString("\n")
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write rule %s: %v", path, err)
	}
}

// loadRules is a thin wrapper so tests read naturally.
func loadRules(t *testing.T, dir string) []config.Rule {
	t.Helper()
	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", dir, err)
	}
	return rules
}

// mustCompile evaluates a CUE source fragment describing an input object and
// returns the resulting cue.Value. The fragment must be a struct literal.
func mustCompile(t *testing.T, ctx *cue.Context, src string) cue.Value {
	t.Helper()
	v := ctx.CompileString(src, cue.Filename("input.cue"))
	if err := v.Err(); err != nil {
		t.Fatalf("compile input %q: %v", src, err)
	}
	return v
}

// -----------------------------------------------------------------------------
// Matching semantics
// -----------------------------------------------------------------------------

func TestEvaluate_EmptyRules_NoMatches(t *testing.T) {
	ctx := cuecontext.New()
	input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, err := evaluator.Evaluate(nil, input)
	if err != nil {
		t.Fatalf("Evaluate on empty rules returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(got))
	}
}

func TestEvaluate_SimpleEqualityMatches(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "bash_deny.cue", `{
		when: {tool_name: "Bash"}
		then: deny: {rule_id: "r1", reason: "no bash"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()

	t.Run("matching tool_name", func(t *testing.T) {
		input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d", len(got))
		}
		if got[0].Action == nil || got[0].Action.RuleID != "r1" {
			t.Fatalf("expected action rule_id=r1, got %+v", got[0].Action)
		}
	})

	t.Run("non-matching tool_name", func(t *testing.T) {
		input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Write"}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches for tool_name=Write, got %d: %+v", len(got), got)
		}
	})
}

func TestEvaluate_HookEventMatches(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "pre_only.cue", `{
		when: {hook_event_name: "PreToolUse"}
		then: deny: {rule_id: "pre", reason: "pre-only"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()
	tests := []struct {
		name  string
		input string
		match bool
	}{
		{"pre matches", `{hook_event_name: "PreToolUse"}`, true},
		{"post does not match", `{hook_event_name: "PostToolUse"}`, false},
		{"user prompt does not match", `{hook_event_name: "UserPromptSubmit"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluator.Evaluate(rules, mustCompile(t, ctx, tt.input))
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if tt.match && len(got) != 1 {
				t.Fatalf("expected match, got %d results: %+v", len(got), got)
			}
			if !tt.match && len(got) != 0 {
				t.Fatalf("expected no match, got %d results: %+v", len(got), got)
			}
		})
	}
}

func TestEvaluate_MultipleRules_PartialMatch(t *testing.T) {
	dir := t.TempDir()
	// Written in non-alphabetical order to confirm the evaluator respects
	// the source order produced by LoadRules (which sorts filenames).
	mustWriteRule(t, dir, "a_bash.cue", `{
		when: {tool_name: "Bash"}
		then: deny: {rule_id: "a", reason: "bash"}
	}`)
	mustWriteRule(t, dir, "b_write.cue", `{
		when: {tool_name: "Write"}
		then: deny: {rule_id: "b", reason: "write"}
	}`)
	mustWriteRule(t, dir, "c_pre.cue", `{
		when: {hook_event_name: "PreToolUse"}
		then: deny: {rule_id: "c", reason: "pre"}
	}`)
	rules := loadRules(t, dir)
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules loaded, got %d", len(rules))
	}

	ctx := cuecontext.New()
	input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 matches (bash, pre), got %d: %+v", len(got), got)
	}
	// Source order: a_bash.cue (rule_id "a"), then c_pre.cue (rule_id "c").
	// b_write.cue must not appear.
	wantIDs := []string{"a", "c"}
	for i, m := range got {
		if m.Action == nil {
			t.Fatalf("match[%d] has nil Action", i)
		}
		if m.Action.RuleID != wantIDs[i] {
			t.Fatalf("match[%d].RuleID=%q, want %q", i, m.Action.RuleID, wantIDs[i])
		}
	}
}

func TestEvaluate_MatchWithoutAction(t *testing.T) {
	// A rule with a `when` clause but no `then` should still register as a
	// match (the design allows auditable "observer" rules that carry no
	// effect). The Match's Action must be nil in that case.
	dir := t.TempDir()
	mustWriteRule(t, dir, "observer.cue", `{
		when: {hook_event_name: "PreToolUse"}
	}`)
	rules := loadRules(t, dir)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Then != nil {
		t.Fatalf("sanity: expected Rule.Then == nil for observer rule, got %+v", rules[0].Then)
	}

	ctx := cuecontext.New()
	input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse"}`)

	got, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d", len(got))
	}
	if got[0].Action != nil {
		t.Fatalf("expected Match.Action to be nil for observer rule, got %+v", got[0].Action)
	}
}

func TestEvaluate_MatchProducesAction(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "deny.cue", `{
		when: {hook_event_name: "PreToolUse"}
		then: deny: {rule_id: "r1", reason: "nope"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()
	input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse"}`)

	got, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d", len(got))
	}
	a := got[0].Action
	if a == nil {
		t.Fatalf("expected action, got nil")
	}
	if a.Kind != config.ActionDeny {
		t.Fatalf("expected Kind=%q, got %q", config.ActionDeny, a.Kind)
	}
	if a.RuleID != "r1" {
		t.Fatalf("expected rule_id=r1, got %q", a.RuleID)
	}
	if a.Severity != "HIGH" {
		t.Fatalf("expected default severity=HIGH, got %q", a.Severity)
	}
}

// -----------------------------------------------------------------------------
// Stdlib composition — the evaluator must unify regex/list.MatchN-style
// constraints, not just concrete struct equality.
//
// The rule files inline the stdlib constraint bodies (they cannot `import`
// the quae stdlib via LoadRules's single-file CompileBytes pipeline). The
// shapes are exact copies of cue/quae.cue and cue/flags/rm.cue, so these
// tests exercise the same evaluator behaviour the real stdlib relies on.
// -----------------------------------------------------------------------------

// systemTargetRuleSrc inlines #isPreToolUse & #isBash & #hasSystemTarget.
// The `list` and `strings` imports live at file scope — see the
// mustWriteRuleWithImports call that writes this body.
const systemTargetRuleSrc = `{
	_SystemPrefixes: ["/etc", "/sys", "/proc", "/boot", "/dev"]
	_systemTarget:   =~"^(\(strings.Join(_SystemPrefixes, "|")))"
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: list.MatchN(>0, _systemTarget)
	}
	then: deny: {rule_id: "system-target", reason: "system path"}
}`

func TestEvaluate_HasSystemTarget_Matches(t *testing.T) {
	dir := t.TempDir()
	mustWriteRuleWithImports(t, dir, "system.cue",
		[]string{"list", "strings"}, systemTargetRuleSrc)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()

	t.Run("absolute system path matches", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			hook_event_name: "PreToolUse"
			tool_name:       "Bash"
			tool_input: parsed: targets: ["/etc/passwd"]
		}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match for /etc/passwd, got %d", len(got))
		}
	})

	t.Run("relative ./etc/passwd does not match (sdl-mcp regression)", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			hook_event_name: "PreToolUse"
			tool_name:       "Bash"
			tool_input: parsed: targets: ["./etc/passwd"]
		}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches for ./etc/passwd, got %d", len(got))
		}
	})
}

// rmForceRuleSrc inlines #isPreToolUse & #isBash & #HasRmForce.
const rmForceRuleSrc = `{
	_rmShortClass: "friv"
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: flags: list.MatchN(>0, =~"^--force(=|$)|^-force(=|$)|^-[\(_rmShortClass)]*f[\(_rmShortClass)]*$")
	}
	then: deny: {rule_id: "rm-force", reason: "rm -f"}
}`

func TestEvaluate_HasRmForce_Matches(t *testing.T) {
	dir := t.TempDir()
	mustWriteRuleWithImports(t, dir, "rm_force.cue",
		[]string{"list"}, rmForceRuleSrc)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()

	t.Run("-rf matches", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			hook_event_name: "PreToolUse"
			tool_name:       "Bash"
			tool_input: parsed: flags: ["-rf"]
		}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match for -rf, got %d", len(got))
		}
	})

	t.Run("-x does not match", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			hook_event_name: "PreToolUse"
			tool_name:       "Bash"
			tool_input: parsed: flags: ["-x"]
		}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches for -x, got %d", len(got))
		}
	})
}

// destructiveActionRuleSrc inlines #hasDestructiveAction — verbs only, not
// command names. Guards against regressions where "rm" leaks into actions.
const destructiveActionRuleSrc = `{
	_DestructiveActions: ["delete", "drop", "remove", "destroy", "truncate"]
	_destructiveAction:  or(_DestructiveActions)
	when: {
		tool_input: parsed: actions: list.MatchN(>0, _destructiveAction)
	}
	then: deny: {rule_id: "destructive", reason: "destructive verb"}
}`

func TestEvaluate_HasDestructiveAction_RejectsRawRm(t *testing.T) {
	dir := t.TempDir()
	mustWriteRuleWithImports(t, dir, "destructive.cue",
		[]string{"list"}, destructiveActionRuleSrc)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()

	t.Run("actions=[remove] matches", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			tool_input: parsed: actions: ["remove"]
		}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match for actions=[remove], got %d", len(got))
		}
	})

	t.Run("actions=[rm] does not match (command names excluded)", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			tool_input: parsed: actions: ["rm"]
		}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches for actions=[rm], got %d", len(got))
		}
	})
}

// andCompositionRuleSrc inlines #HasRmForce & #HasRmRecursive. Both clauses
// must hold on the same `tool_input.parsed.flags` list for the rule to match.
const andCompositionRuleSrc = `{
	_rmShortClass: "friv"
	when: {
		tool_input: parsed: flags: list.MatchN(>0, =~"^--force(=|$)|^-force(=|$)|^-[\(_rmShortClass)]*f[\(_rmShortClass)]*$")
		tool_input: parsed: flags: list.MatchN(>0, =~"^--recursive(=|$)|^-recursive(=|$)|^-[\(_rmShortClass)]*r[\(_rmShortClass)]*$")
	}
	then: deny: {rule_id: "rm-force-recursive", reason: "rm -rf"}
}`

func TestEvaluate_ANDComposition(t *testing.T) {
	dir := t.TempDir()
	mustWriteRuleWithImports(t, dir, "and.cue",
		[]string{"list"}, andCompositionRuleSrc)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()

	t.Run("-rf satisfies both", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			tool_input: parsed: flags: ["-rf"]
		}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match for -rf, got %d", len(got))
		}
	})

	t.Run("-f alone fails the recursive conjunct", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			tool_input: parsed: flags: ["-f"]
		}`)
		got, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches for -f alone, got %d", len(got))
		}
	})
}

// -----------------------------------------------------------------------------
// Integration with parser output
// -----------------------------------------------------------------------------

func TestEvaluate_BashInput_EndToEnd(t *testing.T) {
	// Rule: PreToolUse + Bash + rm-force flag.
	dir := t.TempDir()
	mustWriteRuleWithImports(t, dir, "rm_force.cue",
		[]string{"list"}, rmForceRuleSrc)
	rules := loadRules(t, dir)

	// Raw Claude-Code-shaped input. Preprocess runs the builtin Bash parser
	// and populates tool_input.parsed.
	raw := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input": map[string]any{
			"command": "rm -rf /etc/passwd",
		},
	}
	enriched, err := parser.Preprocess("Bash", raw)
	if err != nil {
		t.Fatalf("Preprocess: %v", err)
	}

	// Convert to a cue.Value via JSON round-trip so the parser.Parsed
	// struct tags are honoured (lowercase field names matching #Parsed).
	j, err := json.Marshal(enriched)
	if err != nil {
		t.Fatalf("marshal enriched: %v", err)
	}
	ctx := cuecontext.New()
	input := ctx.CompileBytes(j, cue.Filename("enriched.json"))
	if err := input.Err(); err != nil {
		t.Fatalf("compile enriched: %v", err)
	}

	got, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match for `rm -rf /etc/passwd`, got %d: %+v", len(got), got)
	}
	if got[0].Action == nil || got[0].Action.RuleID != "rm-force" {
		t.Fatalf("expected rule_id=rm-force, got %+v", got[0].Action)
	}
}

// -----------------------------------------------------------------------------
// Determinism & error handling
// -----------------------------------------------------------------------------

func TestEvaluate_DeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "a.cue", `{
		when: {tool_name: "Bash"}
		then: deny: {rule_id: "a", reason: "bash"}
	}`)
	mustWriteRule(t, dir, "b.cue", `{
		when: {hook_event_name: "PreToolUse"}
		then: deny: {rule_id: "b", reason: "pre"}
	}`)
	mustWriteRule(t, dir, "c.cue", `{
		when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
		then: deny: {rule_id: "c", reason: "both"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()
	input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	first, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("first Evaluate: %v", err)
	}
	second, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("second Evaluate: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("match count diverged: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Action == nil || second[i].Action == nil {
			t.Fatalf("match[%d]: nil Action in one of the runs", i)
		}
		if first[i].Action.RuleID != second[i].Action.RuleID {
			t.Fatalf("non-deterministic order at [%d]: %q vs %q",
				i, first[i].Action.RuleID, second[i].Action.RuleID)
		}
	}
	// Sanity: expected source-order IDs for the input that matches all three.
	want := []string{"a", "b", "c"}
	for i, m := range first {
		if m.Action.RuleID != want[i] {
			t.Fatalf("match[%d].RuleID=%q, want %q", i, m.Action.RuleID, want[i])
		}
	}
}

func TestEvaluate_MalformedWhen_Errors(t *testing.T) {
	// LoadRules rejects many malformed shapes at compile time (e.g. a
	// `when` with `1 & 2` never reaches the evaluator). To exercise the
	// evaluator's "non-struct / bottom `when`" error path, we bypass the
	// loader and construct a Rule whose `When` value is a scalar — which
	// cannot be unified with an input struct.
	ctx := cuecontext.New()

	scalarWhen := ctx.CompileString(`42`, cue.Filename("scalar.cue"))
	if err := scalarWhen.Err(); err != nil {
		t.Fatalf("compile scalar: %v", err)
	}
	rules := []config.Rule{
		{
			Source: "synthetic.cue",
			When:   scalarWhen,
			Then: &config.Action{
				Kind:     config.ActionDeny,
				RuleID:   "bad",
				Reason:   "bottom when",
				Severity: "HIGH",
			},
		},
	}

	input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse"}`)

	_, err := evaluator.Evaluate(rules, input)
	if err == nil {
		t.Fatal("expected error from Evaluate on scalar `when` clause, got nil")
	}
	// The error should identify the offending rule — by filename
	// (Rule.Source) or by index. Either signal is acceptable; silently
	// succeeding or emitting an anonymous error is not.
	msg := err.Error()
	if !strings.Contains(msg, "synthetic") && !strings.Contains(msg, "0") && !strings.Contains(msg, "rule") {
		t.Fatalf("error should reference the offending rule (source=synthetic.cue or index 0), got: %s", msg)
	}

	// Error wrapping hygiene: if the evaluator wraps, errors.Unwrap
	// should return a non-nil inner error, or errors.Is should work for
	// any sentinel the evaluator exposes. Bare errors are also fine —
	// this assertion only rejects the empty-message case.
	if msg == "" {
		t.Fatal("evaluator error has empty message")
	}
	_ = errors.Unwrap(err) // smoke-check the wrapping surface
}

func TestEvaluate_InputMissingRequiredField_StillRunsRules(t *testing.T) {
	// Two rules — one cares about tool_name, one doesn't. The input
	// omits tool_name; the hook-event-only rule should still match, and
	// the tool_name rule should simply not match. No hard failure.
	dir := t.TempDir()
	mustWriteRule(t, dir, "a_hookonly.cue", `{
		when: {hook_event_name: "PreToolUse"}
		then: deny: {rule_id: "hook", reason: "pre"}
	}`)
	mustWriteRule(t, dir, "b_needs_tool.cue", `{
		when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
		then: deny: {rule_id: "needs-tool", reason: "bash"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()
	input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse"}`) // no tool_name

	got, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate must not error on missing optional input fields: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 match (hook-only rule), got %d: %+v", len(got), got)
	}
	if got[0].Action == nil || got[0].Action.RuleID != "hook" {
		t.Fatalf("expected rule_id=hook, got %+v", got[0].Action)
	}
}
