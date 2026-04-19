package envelope_test

import (
	"encoding/json"
	"testing"

	"github.com/srnnkls/quae/internal/envelope"
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
