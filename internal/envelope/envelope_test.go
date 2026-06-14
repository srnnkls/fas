package envelope_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/envelope"
)

func TestCategoryOrdering(t *testing.T) {
	cases := []struct {
		name string
		got  envelope.Category
		want int
	}{
		{"Blocking is zero", envelope.Blocking, 0},
		{"Asking is one", envelope.Asking, 1},
		{"Allowing is two", envelope.Allowing, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if int(tc.got) != tc.want {
				t.Errorf("got %d, want %d", int(tc.got), tc.want)
			}
		})
	}
}

func TestInputRoundTrip(t *testing.T) {
	in := envelope.Input{
		HookEventName: "PreToolUse",
		ToolName:      "Bash",
		ToolInput:     json.RawMessage(`{"command":"ls"}`),
		SessionID:     "sess-1",
		CWD:           "/tmp",
		Signals: map[string]envelope.SignalResult{
			"git": {OK: true, Data: json.RawMessage(`{"branch":"main"}`)},
			"fs":  {OK: false, Err: "permission denied"},
		},
	}

	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out envelope.Input
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.HookEventName != in.HookEventName {
		t.Errorf("HookEventName: got %q, want %q", out.HookEventName, in.HookEventName)
	}
	if out.ToolName != in.ToolName {
		t.Errorf("ToolName: got %q, want %q", out.ToolName, in.ToolName)
	}
	if string(out.ToolInput) != string(in.ToolInput) {
		t.Errorf("ToolInput: got %s, want %s", out.ToolInput, in.ToolInput)
	}
	if out.SessionID != in.SessionID {
		t.Errorf("SessionID: got %q, want %q", out.SessionID, in.SessionID)
	}
	if out.CWD != in.CWD {
		t.Errorf("CWD: got %q, want %q", out.CWD, in.CWD)
	}
	if len(out.Signals) != len(in.Signals) {
		t.Fatalf("Signals len: got %d, want %d", len(out.Signals), len(in.Signals))
	}
	if !out.Signals["git"].OK {
		t.Errorf("Signals[git].OK: got false, want true")
	}
	if out.Signals["fs"].Err != "permission denied" {
		t.Errorf("Signals[fs].Err: got %q, want %q", out.Signals["fs"].Err, "permission denied")
	}
}

func TestInputOmitEmpty(t *testing.T) {
	in := envelope.Input{HookEventName: "SessionStart"}

	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"hook_event_name":"SessionStart"}`
	if string(blob) != want {
		t.Errorf("got %s, want %s", blob, want)
	}
}

func TestSignalResultOmitEmpty(t *testing.T) {
	r := envelope.SignalResult{OK: true}

	blob, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"ok":true}`
	if string(blob) != want {
		t.Errorf("got %s, want %s", blob, want)
	}
}

func TestContinuationIsOrthogonalToCategory(t *testing.T) {
	env := envelope.OutputEnvelope{
		Category: envelope.Allowing,
		Continuation: &envelope.Continuation{
			RuleID: "r3-loop",
			Reason: "finish the migration",
		},
	}

	if env.Category != envelope.Allowing {
		t.Errorf("Category: got %d, want %d (Allowing)", env.Category, envelope.Allowing)
	}
	if env.Continuation == nil {
		t.Fatal("Continuation: got nil, want non-nil alongside Allowing")
	}
	if env.Continuation.RuleID != "r3-loop" {
		t.Errorf("Continuation.RuleID: got %q, want %q", env.Continuation.RuleID, "r3-loop")
	}
	if env.Continuation.Reason != "finish the migration" {
		t.Errorf("Continuation.Reason: got %q, want %q", env.Continuation.Reason, "finish the migration")
	}
}

func TestWithContinuationGuardClearsWhenAlreadyContinuing(t *testing.T) {
	env := envelope.OutputEnvelope{
		Continuation: &envelope.Continuation{RuleID: "r3-loop", Reason: "keep going"},
	}

	guarded := env.WithContinuationGuard(true)

	if guarded.Continuation != nil {
		t.Errorf("Continuation: got %+v, want nil (loop broken)", guarded.Continuation)
	}
}

func TestWithContinuationGuardPreservesWhenNotContinuing(t *testing.T) {
	env := envelope.OutputEnvelope{
		Continuation: &envelope.Continuation{RuleID: "r3-loop", Reason: "keep going"},
	}

	guarded := env.WithContinuationGuard(false)

	if guarded.Continuation == nil {
		t.Fatal("Continuation: got nil, want preserved when not already continuing")
	}
	if guarded.Continuation.RuleID != "r3-loop" {
		t.Errorf("Continuation.RuleID: got %q, want %q", guarded.Continuation.RuleID, "r3-loop")
	}
	if guarded.Continuation.Reason != "keep going" {
		t.Errorf("Continuation.Reason: got %q, want %q", guarded.Continuation.Reason, "keep going")
	}
}

func TestWithContinuationGuardKeepsUnrelatedFields(t *testing.T) {
	env := envelope.OutputEnvelope{
		Category:          envelope.Asking,
		UserReason:        "needs your nod",
		AgentReason:       "policy R3 fired",
		AdditionalContext: "branch=main",
		UpdatedInput:      json.RawMessage(`{"command":"ls"}`),
		Continuation:      &envelope.Continuation{RuleID: "r3-loop", Reason: "keep going"},
	}

	guarded := env.WithContinuationGuard(true)

	if guarded.Continuation != nil {
		t.Errorf("Continuation: got %+v, want nil after guard", guarded.Continuation)
	}
	if guarded.Category != envelope.Asking {
		t.Errorf("Category: got %d, want %d (Asking)", guarded.Category, envelope.Asking)
	}
	if guarded.UserReason != "needs your nod" {
		t.Errorf("UserReason: got %q, want %q", guarded.UserReason, "needs your nod")
	}
	if guarded.AgentReason != "policy R3 fired" {
		t.Errorf("AgentReason: got %q, want %q", guarded.AgentReason, "policy R3 fired")
	}
	if guarded.AdditionalContext != "branch=main" {
		t.Errorf("AdditionalContext: got %q, want %q", guarded.AdditionalContext, "branch=main")
	}
	if string(guarded.UpdatedInput) != `{"command":"ls"}` {
		t.Errorf("UpdatedInput: got %s, want %s", guarded.UpdatedInput, `{"command":"ls"}`)
	}
}

func TestInputStopHookActiveRoundTrip(t *testing.T) {
	in := envelope.Input{HookEventName: "Stop", StopHookActive: true}

	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(blob), `"stop_hook_active":true`) {
		t.Errorf("marshal: %s does not contain %q", blob, `"stop_hook_active":true`)
	}

	var out envelope.Input
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.StopHookActive {
		t.Errorf("StopHookActive: got false, want true")
	}
}

func TestInputStopHookActiveOmitEmpty(t *testing.T) {
	in := envelope.Input{HookEventName: "Stop", StopHookActive: false}

	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "stop_hook_active") {
		t.Errorf("marshal: %s should omit stop_hook_active when false", blob)
	}
}
