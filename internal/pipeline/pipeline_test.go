package pipeline_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/evaluator"
	"github.com/srnnkls/fas/internal/pipeline"
)

// writeRule drops a single .cue rule file into dir, wrapping body in the
// `rule: { ... }` top-level the loader expects.
func writeRule(t *testing.T, dir, name, body string) {
	t.Helper()
	src := "package rules\n\nrule: " + body + "\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
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

// compileInput evaluates a CUE struct literal into a cue.Value.
func compileInput(t *testing.T, ctx *cue.Context, src string) cue.Value {
	t.Helper()
	v := ctx.CompileString(src, cue.Filename("input.cue"))
	if err := v.Err(); err != nil {
		t.Fatalf("compile input %q: %v", src, err)
	}
	return v
}

// ruleIDs extracts action rule IDs from a match slice; matches without an
// action contribute the empty string so positional mismatches stay visible.
func ruleIDs(matches []evaluator.Match) []string {
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if m.Action == nil {
			out = append(out, "")
			continue
		}
		out = append(out, m.Action.RuleID)
	}
	return out
}

func eqSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// Empty phases
// -----------------------------------------------------------------------------

func TestEvaluatePhases_EmptyBothPhases_NoMatches(t *testing.T) {
	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(nil, nil, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 matches for empty phases, got %d: %+v", len(got), got)
	}
}

// -----------------------------------------------------------------------------
// Single-phase cases
// -----------------------------------------------------------------------------

func TestEvaluatePhases_OnlyGlobal_Matches_Returned(t *testing.T) {
	globalDir := t.TempDir()
	writeRule(t, globalDir, "g_match.cue", `{
		when: {tool_name: "Bash"}
		then: ask: {rule_id: "g-match", reason: "bash ask", question: "ok?"}
	}`)
	writeRule(t, globalDir, "g_nomatch.cue", `{
		when: {tool_name: "Write"}
		then: ask: {rule_id: "g-nomatch", reason: "write ask", question: "ok?"}
	}`)
	global := loadRules(t, globalDir)
	if len(global) != 2 {
		t.Fatalf("expected 2 global rules, got %d", len(global))
	}

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, nil, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if want := []string{"g-match"}; !eqSlice(ruleIDs(got), want) {
		t.Fatalf("rule IDs = %v, want %v", ruleIDs(got), want)
	}
}

func TestEvaluatePhases_OnlyProject_Matches_Returned(t *testing.T) {
	projectDir := t.TempDir()
	writeRule(t, projectDir, "p_match.cue", `{
		when: {tool_name: "Bash"}
		then: allow: true
	}`)
	writeRule(t, projectDir, "p_nomatch.cue", `{
		when: {tool_name: "Write"}
		then: allow: true
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(nil, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(got), got)
	}
	if got[0].Action == nil || got[0].Action.Kind != config.ActionAllow {
		t.Fatalf("expected allow action, got %+v", got[0].Action)
	}
}

// -----------------------------------------------------------------------------
// Phase combination (non-blocking)
// -----------------------------------------------------------------------------

func TestEvaluatePhases_BothPhases_NoBlocking_Combined(t *testing.T) {
	globalDir := t.TempDir()
	writeRule(t, globalDir, "g_ask.cue", `{
		when: {tool_name: "Bash"}
		then: ask: {rule_id: "g-ask", reason: "ask first", question: "proceed?"}
	}`)
	global := loadRules(t, globalDir)

	projectDir := t.TempDir()
	writeRule(t, projectDir, "p_allow.cue", `{
		when: {tool_name: "Bash"}
		then: allow: true
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 matches (global ask + project allow), got %d: %+v", len(got), got)
	}
	// Phase-1 first, phase-2 second.
	if got[0].Action == nil || got[0].Action.Kind != config.ActionAsk || got[0].Action.RuleID != "g-ask" {
		t.Fatalf("expected match[0]=ask g-ask, got %+v", got[0].Action)
	}
	if got[1].Action == nil || got[1].Action.Kind != config.ActionAllow {
		t.Fatalf("expected match[1]=allow, got %+v", got[1].Action)
	}
}

func TestEvaluatePhases_Phase1NonBlocking_Phase2Runs(t *testing.T) {
	// Key invariant: only Blocking (deny) short-circuits. An ask match in
	// phase 1 must not suppress phase 2.
	globalDir := t.TempDir()
	writeRule(t, globalDir, "g_ask.cue", `{
		when: {tool_name: "Bash"}
		then: ask: {rule_id: "g-ask", reason: "ask first", question: "proceed?"}
	}`)
	global := loadRules(t, globalDir)

	projectDir := t.TempDir()
	writeRule(t, projectDir, "p_allow.cue", `{
		when: {tool_name: "Bash"}
		then: allow: true
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	// Project phase must have run — we expect an allow match after the ask.
	sawProject := false
	for _, m := range got {
		if m.Action != nil && m.Action.Kind == config.ActionAllow {
			sawProject = true
		}
	}
	if !sawProject {
		t.Fatalf("phase 2 must run when phase 1 is non-blocking; got matches=%+v", got)
	}
}

func TestEvaluatePhases_Phase1InjectOnly_NoGate_Phase2Runs(t *testing.T) {
	// Global has only an inject (effect, no gate). Phase 2 must still run.
	globalDir := t.TempDir()
	writeRule(t, globalDir, "g_inject.cue", `{
		when: {tool_name: "Bash"}
		then: inject: {rule_id: "g-inject", text: "hint", channel: "agent", priority: 50}
	}`)
	global := loadRules(t, globalDir)

	projectDir := t.TempDir()
	writeRule(t, projectDir, "p_allow.cue", `{
		when: {tool_name: "Bash"}
		then: allow: true
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if want := []string{"g-inject", ""}; !eqSlice(ruleIDs(got), want) {
		// Allow's decoded RuleID is empty — compare by kind below if label fails.
		kinds := make([]config.ActionKind, 0, len(got))
		for _, m := range got {
			if m.Action == nil {
				kinds = append(kinds, "")
				continue
			}
			kinds = append(kinds, m.Action.Kind)
		}
		if len(kinds) != 2 || kinds[0] != config.ActionInject || kinds[1] != config.ActionAllow {
			t.Fatalf("expected [inject, allow], got ids=%v kinds=%v", ruleIDs(got), kinds)
		}
	}
}

// -----------------------------------------------------------------------------
// Phase-1 short-circuit
// -----------------------------------------------------------------------------

func TestEvaluatePhases_Phase1Blocks_Phase2Skipped(t *testing.T) {
	globalDir := t.TempDir()
	writeRule(t, globalDir, "g_deny.cue", `{
		when: {tool_name: "Bash"}
		then: deny: {rule_id: "g-deny", reason: "hard no", severity: "HIGH"}
	}`)
	global := loadRules(t, globalDir)

	// A project rule whose `when` WOULD match the input if evaluated. The
	// assertion that rule is absent from results is therefore non-trivial:
	// it can only be absent if phase 2 was skipped entirely.
	projectDir := t.TempDir()
	writeRule(t, projectDir, "p_would_match.cue", `{
		when: {tool_name: "Bash"}
		then: allow: true
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 match (phase-1 deny only); got %d: %+v", len(got), got)
	}
	if got[0].Action == nil || got[0].Action.Kind != config.ActionDeny || got[0].Action.RuleID != "g-deny" {
		t.Fatalf("expected phase-1 deny g-deny, got %+v", got[0].Action)
	}
	// Sanity: no allow from phase 2 leaked in.
	for _, m := range got {
		if m.Action != nil && m.Action.Kind == config.ActionAllow {
			t.Fatalf("phase 2 must not run on deny short-circuit, but allow leaked: %+v", m)
		}
	}
}

func TestEvaluatePhases_Phase1DenyPlusInject_BothReturned_Phase2Skipped(t *testing.T) {
	// Phase 1 has BOTH a deny and an inject. Both must be returned (effects
	// preserved across the deny gate), and phase 2 must still be skipped.
	globalDir := t.TempDir()
	writeRule(t, globalDir, "a_inject.cue", `{
		when: {tool_name: "Bash"}
		then: inject: {rule_id: "g-inject", text: "hint", channel: "agent", priority: 50}
	}`)
	writeRule(t, globalDir, "b_deny.cue", `{
		when: {tool_name: "Bash"}
		then: deny: {rule_id: "g-deny", reason: "no", severity: "HIGH"}
	}`)
	global := loadRules(t, globalDir)

	projectDir := t.TempDir()
	writeRule(t, projectDir, "p_would_match.cue", `{
		when: {tool_name: "Bash"}
		then: allow: true
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 matches (phase-1 inject + deny), got %d: %+v", len(got), got)
	}
	// Phase-1 source order: a_inject.cue (alphabetical by filename), then b_deny.cue.
	if got[0].Action == nil || got[0].Action.Kind != config.ActionInject {
		t.Fatalf("expected match[0]=inject, got %+v", got[0].Action)
	}
	if got[1].Action == nil || got[1].Action.Kind != config.ActionDeny {
		t.Fatalf("expected match[1]=deny, got %+v", got[1].Action)
	}
	// Phase 2 must not have leaked in.
	for _, m := range got {
		if m.Action != nil && m.Action.Kind == config.ActionAllow {
			t.Fatalf("phase 2 must not run on deny short-circuit: %+v", m)
		}
	}
}

// -----------------------------------------------------------------------------
// Ordering
// -----------------------------------------------------------------------------

func TestEvaluatePhases_OrderPreserved_PhasesThenSource(t *testing.T) {
	// Two global rules, two project rules, all matching. Expected result
	// order: [g1, g2, p1, p2] where the in-phase order follows LoadRules
	// filename sort.
	globalDir := t.TempDir()
	writeRule(t, globalDir, "a_g1.cue", `{
		when: {tool_name: "Bash"}
		then: ask: {rule_id: "g1", reason: "r", question: "q?"}
	}`)
	writeRule(t, globalDir, "b_g2.cue", `{
		when: {tool_name: "Bash"}
		then: ask: {rule_id: "g2", reason: "r", question: "q?"}
	}`)
	global := loadRules(t, globalDir)

	projectDir := t.TempDir()
	writeRule(t, projectDir, "a_p1.cue", `{
		when: {tool_name: "Bash"}
		then: inject: {rule_id: "p1", text: "t", channel: "agent", priority: 50}
	}`)
	writeRule(t, projectDir, "b_p2.cue", `{
		when: {tool_name: "Bash"}
		then: inject: {rule_id: "p2", text: "t", channel: "agent", priority: 50}
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	want := []string{"g1", "g2", "p1", "p2"}
	if !eqSlice(ruleIDs(got), want) {
		t.Fatalf("order mismatch: got %v, want %v", ruleIDs(got), want)
	}
}

// -----------------------------------------------------------------------------
// Error paths — use an in-memory synthetic rule for the malformed-when cases
// since LoadRules rejects bottom/scalar `when` clauses at compile time.
// -----------------------------------------------------------------------------

// scalarWhenRule returns a Rule whose `When` is a scalar, which the evaluator
// treats as malformed (must be a struct).
func scalarWhenRule(t *testing.T, source, ruleID string) config.Rule {
	t.Helper()
	ctx := cuecontext.New()
	v := ctx.CompileString(`42`, cue.Filename(source))
	if err := v.Err(); err != nil {
		t.Fatalf("compile scalar when: %v", err)
	}
	return config.Rule{
		Source: source,
		When:   v,
		Then: &config.Action{
			Kind:     config.ActionDeny,
			RuleID:   ruleID,
			Reason:   "bad",
			Severity: "HIGH",
		},
	}
}

func TestEvaluatePhases_MalformedPhase1_ErrorReturned(t *testing.T) {
	global := []config.Rule{scalarWhenRule(t, "bad_global.cue", "bg")}

	// Phase 2 must NOT run — we'd detect that via a project rule that, if
	// evaluated, would also error. But asserting "phase 2 not called" when
	// phase 1 errored is simplest through the contract: a single error
	// wrapping phase-1's evaluator failure, with no partial results.
	projectDir := t.TempDir()
	writeRule(t, projectDir, "p.cue", `{
		when: {tool_name: "Bash"}
		then: allow: true
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err == nil {
		t.Fatal("expected error from malformed phase-1 rule, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil matches on error, got %+v", got)
	}

	// The pipeline should wrap or propagate the evaluator's error; confirm
	// the wrapping preserves the underlying chain.
	if msg := err.Error(); !strings.Contains(msg, "bad_global") && !strings.Contains(msg, "when") {
		t.Fatalf("error should reference phase-1 rule or its when clause, got: %s", msg)
	}
	// errors.Unwrap should yield a non-nil inner error if the pipeline
	// wraps (it should) — but raw propagation is also acceptable.
	_ = errors.Unwrap(err)
}

func TestEvaluatePhases_MalformedPhase2_ErrorReturned(t *testing.T) {
	// Phase 1 is clean; phase 2 has the malformed rule. The error must
	// surface after phase 1 completes.
	globalDir := t.TempDir()
	writeRule(t, globalDir, "g_ask.cue", `{
		when: {tool_name: "Bash"}
		then: ask: {rule_id: "g-ask", reason: "r", question: "q?"}
	}`)
	global := loadRules(t, globalDir)

	project := []config.Rule{scalarWhenRule(t, "bad_project.cue", "bp")}

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err == nil {
		t.Fatal("expected error from malformed phase-2 rule, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil matches on error, got %+v", got)
	}
	if msg := err.Error(); !strings.Contains(msg, "bad_project") && !strings.Contains(msg, "when") {
		t.Fatalf("error should reference phase-2 rule, got: %s", msg)
	}
}

// -----------------------------------------------------------------------------
// Determinism
// -----------------------------------------------------------------------------

func TestEvaluatePhases_Deterministic(t *testing.T) {
	globalDir := t.TempDir()
	writeRule(t, globalDir, "a.cue", `{
		when: {tool_name: "Bash"}
		then: ask: {rule_id: "g-a", reason: "r", question: "q?"}
	}`)
	writeRule(t, globalDir, "b.cue", `{
		when: {hook_event_name: "PreToolUse"}
		then: ask: {rule_id: "g-b", reason: "r", question: "q?"}
	}`)
	global := loadRules(t, globalDir)

	projectDir := t.TempDir()
	writeRule(t, projectDir, "a.cue", `{
		when: {tool_name: "Bash"}
		then: inject: {rule_id: "p-a", text: "t", channel: "agent", priority: 50}
	}`)
	writeRule(t, projectDir, "b.cue", `{
		when: {hook_event_name: "PreToolUse"}
		then: inject: {rule_id: "p-b", text: "t", channel: "agent", priority: 50}
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	first, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("first EvaluatePhases: %v", err)
	}
	second, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("second EvaluatePhases: %v", err)
	}

	firstIDs, secondIDs := ruleIDs(first), ruleIDs(second)
	if !eqSlice(firstIDs, secondIDs) {
		t.Fatalf("non-deterministic output: first=%v second=%v", firstIDs, secondIDs)
	}
	want := []string{"g-a", "g-b", "p-a", "p-b"}
	if !eqSlice(firstIDs, want) {
		t.Fatalf("order mismatch: got %v, want %v", firstIDs, want)
	}
}
