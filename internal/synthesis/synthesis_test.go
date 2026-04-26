package synthesis_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/envelope"
	"github.com/srnnkls/fas/internal/evaluator"
	"github.com/srnnkls/fas/internal/synthesis"
)

// -----------------------------------------------------------------------------
// Test helpers — action and match constructors. These are the minimum surface
// the synthesizer consumes; they exist so each test body stays focused on the
// invariant under test rather than boilerplate.
// -----------------------------------------------------------------------------

func deny(ruleID, reason, severity string) evaluator.Match {
	return evaluator.Match{
		Rule: config.Rule{Source: ruleID + ".cue"},
		Action: &config.Action{
			Kind:     config.ActionDeny,
			RuleID:   ruleID,
			Reason:   reason,
			Severity: severity,
		},
	}
}

func ask(ruleID, reason, question string) evaluator.Match {
	return evaluator.Match{
		Rule: config.Rule{Source: ruleID + ".cue"},
		Action: &config.Action{
			Kind:     config.ActionAsk,
			RuleID:   ruleID,
			Reason:   reason,
			Question: question,
		},
	}
}

func allow(ruleID string) evaluator.Match {
	return evaluator.Match{
		Rule: config.Rule{Source: ruleID + ".cue"},
		Action: &config.Action{
			Kind:   config.ActionAllow,
			RuleID: ruleID,
			Allow:  true,
		},
	}
}

func inject(ruleID, text, channel string, priority int) evaluator.Match {
	return evaluator.Match{
		Rule: config.Rule{Source: ruleID + ".cue"},
		Action: &config.Action{
			Kind:     config.ActionInject,
			RuleID:   ruleID,
			Text:     text,
			Channel:  channel,
			Priority: priority,
		},
	}
}

func modify(ruleID, reason, mode string, priority int, updated map[string]any) evaluator.Match {
	return evaluator.Match{
		Rule: config.Rule{Source: ruleID + ".cue"},
		Action: &config.Action{
			Kind:         config.ActionModify,
			RuleID:       ruleID,
			Reason:       reason,
			Mode:         mode,
			Priority:     priority,
			UpdatedInput: updated,
		},
	}
}

// -----------------------------------------------------------------------------
// Empty / trivial inputs
// -----------------------------------------------------------------------------

func TestSynthesize_Empty_NoMatches_AllowingZero(t *testing.T) {
	got := synthesis.Synthesize(nil, 0)
	want := envelope.OutputEnvelope{Category: envelope.Allowing}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("empty input must produce zero-value Allowing envelope (-want +got):\n%s", diff)
	}
}

// -----------------------------------------------------------------------------
// Single-gate scenarios
// -----------------------------------------------------------------------------

func TestSynthesize_SingleDeny_Blocking(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		deny("d1", "nope", "HIGH"),
	}, 0)

	if got.Category != envelope.Blocking {
		t.Fatalf("Category=%v, want Blocking", got.Category)
	}
	if got.UserReason != "nope" {
		t.Errorf("UserReason=%q, want %q (from winning Deny.Reason)", got.UserReason, "nope")
	}
}

func TestSynthesize_AskOnly_Asking(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		ask("a1", "need confirmation", "proceed?"),
	}, 0)

	if got.Category != envelope.Asking {
		t.Fatalf("Category=%v, want Asking", got.Category)
	}
	// Ask contributes Question to UserReason (design.md §Synthesizer: UserReason
	// / AgentReason "come from the winning gate action"). The exact composition
	// is implementation-defined, but the question text MUST be visible to the
	// user — otherwise the user cannot answer the prompt.
	if !strings.Contains(got.UserReason, "proceed?") {
		t.Errorf("UserReason=%q must include Ask.Question %q", got.UserReason, "proceed?")
	}
}

func TestSynthesize_AllowOnly_Allowing(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		allow("ok"),
	}, 0)

	if got.Category != envelope.Allowing {
		t.Fatalf("Category=%v, want Allowing", got.Category)
	}
}

// -----------------------------------------------------------------------------
// Gate priority & tie-breaking
// -----------------------------------------------------------------------------

func TestSynthesize_MultipleDenies_HigherSeverityWins(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		deny("d-high", "high msg", "HIGH"),
		deny("d-crit", "crit msg", "CRITICAL"),
		deny("d-med", "med msg", "MEDIUM"),
	}, 0)

	if got.Category != envelope.Blocking {
		t.Fatalf("Category=%v, want Blocking", got.Category)
	}
	if got.UserReason != "crit msg" {
		t.Errorf("UserReason=%q, want %q (CRITICAL must beat HIGH and MEDIUM)",
			got.UserReason, "crit msg")
	}
}

func TestSynthesize_MultipleDenies_SameSeverity_RuleIDTiebreak(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		deny("zzz", "zzz msg", "HIGH"),
		deny("aaa", "aaa msg", "HIGH"),
		deny("mmm", "mmm msg", "HIGH"),
	}, 0)

	if got.UserReason != "aaa msg" {
		t.Errorf("UserReason=%q, want %q (lowest rule_id ASC must win on severity tie)",
			got.UserReason, "aaa msg")
	}
}

func TestSynthesize_DenyPlusAsk_DenyWins(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		ask("a1", "ask reason", "ok?"),
		deny("d1", "deny reason", "HIGH"),
	}, 0)

	if got.Category != envelope.Blocking {
		t.Fatalf("Category=%v, want Blocking (deny > ask)", got.Category)
	}
	if got.UserReason != "deny reason" {
		t.Errorf("UserReason=%q, want %q", got.UserReason, "deny reason")
	}
}

func TestSynthesize_AskPlusAllow_AskWins(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		allow("ok1"),
		ask("a1", "check first", "go?"),
		allow("ok2"),
	}, 0)

	if got.Category != envelope.Asking {
		t.Fatalf("Category=%v, want Asking (ask > allow)", got.Category)
	}
}

func TestSynthesize_MultipleAsks_RuleIDTiebreak(t *testing.T) {
	// Ask has no severity; tiebreak by rule_id ASC.
	got := synthesis.Synthesize([]evaluator.Match{
		ask("zzz", "zzz reason", "zzz?"),
		ask("aaa", "aaa reason", "aaa?"),
	}, 0)

	if got.Category != envelope.Asking {
		t.Fatalf("Category=%v, want Asking", got.Category)
	}
	if !strings.Contains(got.UserReason, "aaa") {
		t.Errorf("UserReason=%q, want text from ask with lowest rule_id (aaa)", got.UserReason)
	}
}

// -----------------------------------------------------------------------------
// Inject aggregation
// -----------------------------------------------------------------------------

func TestSynthesize_InjectOnly_Allowing_WithAdditionalContext(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		inject("i1", "hello world", "agent", 50),
	}, 0)

	if got.Category != envelope.Allowing {
		t.Fatalf("Category=%v, want Allowing (injects don't set a gate)", got.Category)
	}
	if !strings.Contains(got.AdditionalContext, "hello world") {
		t.Errorf("AdditionalContext=%q must contain inject text %q",
			got.AdditionalContext, "hello world")
	}
}

func TestSynthesize_Inject_PriorityOrder(t *testing.T) {
	// Higher priority must appear before lower priority in the concatenated
	// agent-channel output.
	got := synthesis.Synthesize([]evaluator.Match{
		inject("low", "LOW_TEXT", "agent", 10),
		inject("hi", "HIGH_TEXT", "agent", 90),
		inject("mid", "MID_TEXT", "agent", 50),
	}, 0)

	ctx := got.AdditionalContext
	hi := strings.Index(ctx, "HIGH_TEXT")
	mid := strings.Index(ctx, "MID_TEXT")
	low := strings.Index(ctx, "LOW_TEXT")
	if hi == -1 || mid == -1 || low == -1 {
		t.Fatalf("AdditionalContext missing one of the inject bodies: %q", ctx)
	}
	if hi >= mid || mid >= low {
		t.Errorf("inject order wrong: got HIGH@%d MID@%d LOW@%d in %q; want HIGH < MID < LOW",
			hi, mid, low, ctx)
	}
}

func TestSynthesize_Inject_SamePriority_RuleIDTiebreak(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		inject("zzz", "Z_TEXT", "agent", 50),
		inject("aaa", "A_TEXT", "agent", 50),
	}, 0)

	ctx := got.AdditionalContext
	a := strings.Index(ctx, "A_TEXT")
	z := strings.Index(ctx, "Z_TEXT")
	if a == -1 || z == -1 {
		t.Fatalf("AdditionalContext missing inject bodies: %q", ctx)
	}
	if a >= z {
		t.Errorf("same-priority inject tiebreak wrong: aaa should precede zzz; got %q", ctx)
	}
}

func TestSynthesize_Inject_DedupByRuleID(t *testing.T) {
	// Two injects with the same rule_id: the first occurrence wins; the
	// second's text must not appear at all.
	got := synthesis.Synthesize([]evaluator.Match{
		inject("dup", "FIRST_TEXT", "agent", 50),
		inject("dup", "SECOND_TEXT", "agent", 50),
	}, 0)

	if !strings.Contains(got.AdditionalContext, "FIRST_TEXT") {
		t.Errorf("expected FIRST_TEXT in AdditionalContext, got %q", got.AdditionalContext)
	}
	if strings.Contains(got.AdditionalContext, "SECOND_TEXT") {
		t.Errorf("duplicate rule_id should be dropped; SECOND_TEXT must NOT appear in %q",
			got.AdditionalContext)
	}
}

func TestSynthesize_Inject_SizeBudgetTruncation(t *testing.T) {
	// A budget smaller than the total injected bytes must cause later
	// (lower-priority) injects to be truncated or dropped. The highest-priority
	// inject must survive; the lowest must not appear in full.
	const budget = 20 // bytes
	got := synthesis.Synthesize([]evaluator.Match{
		inject("hi", "AAAAAAAAAA", "agent", 90), // 10 bytes, priority 90
		inject("lo", "BBBBBBBBBB", "agent", 10), // 10 bytes, priority 10
		inject("xx", "CCCCCCCCCC", "agent", 5),  // 10 bytes, priority 5 — must be excluded
	}, budget)

	if !strings.Contains(got.AdditionalContext, "AAAAAAAAAA") {
		t.Errorf("highest-priority inject must survive the budget; got %q", got.AdditionalContext)
	}
	if strings.Contains(got.AdditionalContext, "CCCCCCCCCC") {
		t.Errorf("lowest-priority inject must be excluded under budget=%d; got %q",
			budget, got.AdditionalContext)
	}
	if len(got.AdditionalContext) > budget {
		t.Errorf("AdditionalContext length %d exceeds sizeBudget %d",
			len(got.AdditionalContext), budget)
	}
}

func TestSynthesize_Inject_AgentChannel_GoesToAdditionalContext(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		inject("i1", "agent_text", "agent", 50),
	}, 0)

	if !strings.Contains(got.AdditionalContext, "agent_text") {
		t.Errorf("agent-channel inject must land in AdditionalContext, got %q",
			got.AdditionalContext)
	}
	if strings.Contains(got.UserReason, "agent_text") {
		t.Errorf("agent-channel inject must NOT leak into UserReason, got %q", got.UserReason)
	}
}

func TestSynthesize_Inject_UserChannel_GoesToUserReason(t *testing.T) {
	// The contract gives us a choice; this test documents it: user-channel
	// injects append to UserReason (newline-separated from any gate reason).
	got := synthesis.Synthesize([]evaluator.Match{
		inject("i1", "user_text", "user", 50),
	}, 0)

	if !strings.Contains(got.UserReason, "user_text") {
		t.Errorf("user-channel inject must land in UserReason, got %q", got.UserReason)
	}
	if strings.Contains(got.AdditionalContext, "user_text") {
		t.Errorf("user-channel inject must NOT leak into AdditionalContext, got %q",
			got.AdditionalContext)
	}
}

// -----------------------------------------------------------------------------
// Modify interactions with gates
// -----------------------------------------------------------------------------

func TestSynthesize_ModifyInBlocking_UpdatedInputDropped(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		deny("d1", "blocked", "HIGH"),
		modify("m1", "rewrite", "silent", 50, map[string]any{"foo": "bar"}),
	}, 0)

	if got.Category != envelope.Blocking {
		t.Fatalf("Category=%v, want Blocking", got.Category)
	}
	if got.UpdatedInput != nil {
		t.Errorf("UpdatedInput must be nil when Category is Blocking; got %s",
			string(got.UpdatedInput))
	}
}

func TestSynthesize_ModifyInAsking_UpdatedInputSet(t *testing.T) {
	want := map[string]any{"command": "rm -i target"}
	got := synthesis.Synthesize([]evaluator.Match{
		ask("a1", "confirm?", "really?"),
		modify("m1", "safer variant", "silent", 50, want),
	}, 0)

	if got.Category != envelope.Asking {
		t.Fatalf("Category=%v, want Asking", got.Category)
	}
	if len(got.UpdatedInput) == 0 {
		t.Fatalf("UpdatedInput must be populated when Category is Asking; got empty")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got.UpdatedInput, &decoded); err != nil {
		t.Fatalf("UpdatedInput is not valid JSON: %v (raw=%s)", err, string(got.UpdatedInput))
	}
	if diff := cmp.Diff(want, decoded); diff != "" {
		t.Errorf("UpdatedInput payload mismatch (-want +got):\n%s", diff)
	}
}

func TestSynthesize_ModifyOnly_Confirm_Asking(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		modify("m1", "ask user", "confirm", 50, map[string]any{"x": 1}),
	}, 0)

	if got.Category != envelope.Asking {
		t.Fatalf("Category=%v, want Asking (modify mode=confirm with no gate)", got.Category)
	}
	if len(got.UpdatedInput) == 0 {
		t.Fatal("UpdatedInput must be populated in Asking category")
	}
}

func TestSynthesize_ModifyOnly_Silent_Allowing(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		modify("m1", "rewrite silently", "silent", 50, map[string]any{"x": 1}),
	}, 0)

	if got.Category != envelope.Allowing {
		t.Fatalf("Category=%v, want Allowing (modify mode=silent with no gate)", got.Category)
	}
	if len(got.UpdatedInput) == 0 {
		t.Fatal("UpdatedInput must be populated in Allowing category")
	}
}

func TestSynthesize_Modify_HighestPriorityWins(t *testing.T) {
	winner := map[string]any{"chosen": "hi"}
	got := synthesis.Synthesize([]evaluator.Match{
		modify("lo", "low", "silent", 10, map[string]any{"chosen": "lo"}),
		modify("hi", "high", "silent", 90, winner),
		modify("mid", "mid", "silent", 50, map[string]any{"chosen": "mid"}),
	}, 0)

	var decoded map[string]any
	if err := json.Unmarshal(got.UpdatedInput, &decoded); err != nil {
		t.Fatalf("UpdatedInput not valid JSON: %v (raw=%s)", err, string(got.UpdatedInput))
	}
	if diff := cmp.Diff(winner, decoded); diff != "" {
		t.Errorf("highest-priority modify must win (-want +got):\n%s", diff)
	}
}

func TestSynthesize_Modify_SamePriority_RuleIDTiebreak(t *testing.T) {
	winner := map[string]any{"chosen": "aaa"}
	got := synthesis.Synthesize([]evaluator.Match{
		modify("zzz", "z", "silent", 50, map[string]any{"chosen": "zzz"}),
		modify("aaa", "a", "silent", 50, winner),
	}, 0)

	var decoded map[string]any
	if err := json.Unmarshal(got.UpdatedInput, &decoded); err != nil {
		t.Fatalf("UpdatedInput not valid JSON: %v (raw=%s)", err, string(got.UpdatedInput))
	}
	if diff := cmp.Diff(winner, decoded); diff != "" {
		t.Errorf("same-priority modify tiebreak by rule_id ASC (-want +got):\n%s", diff)
	}
}

// -----------------------------------------------------------------------------
// Observer rules (nil Action) must be tolerated, not crash the synthesizer.
// -----------------------------------------------------------------------------

func TestSynthesize_NilActionMatchesIgnored(t *testing.T) {
	got := synthesis.Synthesize([]evaluator.Match{
		{Rule: config.Rule{Source: "observer.cue"}, Action: nil},
		deny("d1", "blocked", "HIGH"),
	}, 0)

	if got.Category != envelope.Blocking {
		t.Fatalf("Category=%v, want Blocking (nil-action match must not block deny)", got.Category)
	}
	if got.UserReason != "blocked" {
		t.Errorf("UserReason=%q, want %q", got.UserReason, "blocked")
	}
}

// -----------------------------------------------------------------------------
// Determinism — same input run twice must yield identical envelopes.
// -----------------------------------------------------------------------------

func TestSynthesize_Deterministic(t *testing.T) {
	matches := []evaluator.Match{
		inject("i-b", "B", "agent", 50),
		inject("i-a", "A", "agent", 90),
		inject("i-c", "C", "user", 50),
		modify("m-z", "mz", "silent", 50, map[string]any{"k": "z"}),
		modify("m-a", "ma", "silent", 50, map[string]any{"k": "a"}),
		ask("q1", "why?", "really?"),
	}

	first := synthesis.Synthesize(matches, 0)
	second := synthesis.Synthesize(matches, 0)

	if diff := cmp.Diff(first, second); diff != "" {
		t.Errorf("Synthesize is non-deterministic (-first +second):\n%s", diff)
	}
}
