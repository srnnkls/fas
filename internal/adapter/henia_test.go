package adapter_test

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/srnnkls/fas/internal/adapter"
	"github.com/srnnkls/fas/internal/envelope"
)

func TestHeniaParseInput(t *testing.T) {
	a := adapter.Henia{}
	if a.Name() != "henia" || !a.AllowsModify() {
		t.Fatal("incorrect adapter capabilities")
	}
	raw := json.RawMessage(`{"command":"git status \"x\"","skill":"status","source":"tropos","tier":"global","caller":"claude","cwd":"/repo","extra":1}`)
	got, err := a.ParseInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := &envelope.Input{
		HookEventName: "PreToolUse",
		ToolName:      "Bash",
		ToolInput:     json.RawMessage(`{"command":"git status \"x\""}`),
		CWD:           "/repo",
		Henia:         &envelope.Henia{Skill: "status", Source: "tropos", Tier: "global", Caller: "claude"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	for _, raw := range []string{
		`[]`, `"s"`, `null`, `{`,
		`{"skill":"s","source":"p","tier":"project"}`,
		`{"command":"  ","skill":"s","source":"p","tier":"project"}`,
		`{"command":"ls","source":"p","tier":"project"}`,
		`{"command":"ls","skill":"s","tier":"project"}`,
		`{"command":"ls","skill":"s","source":"p"}`,
		`{"command":"ls","skill":"s","source":"p","tier":"user"}`,
		`{"command":42,"skill":"s","source":"p","tier":"project"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := a.ParseInput(json.RawMessage(raw)); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestHeniaRenderOutput(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category envelope.Category
		rule     string
		reason   string
		update   string
		want     string
	}{
		{"allow", envelope.Allowing, "", "", "", `{"decision":"allow"}`},
		{"allow rewrite", envelope.Allowing, "", "", `{"command":"git --no-optional-locks status"}`, `{"decision":"allow","command":"git --no-optional-locks status"}`},
		{"allow non-command update", envelope.Allowing, "", "", `{"path":"x"}`, `{"decision":"allow"}`},
		{"allow blank command update", envelope.Allowing, "", "", `{"command":" "}`, `{"decision":"allow"}`},
		{"deny", envelope.Blocking, "r1", "no", "", `{"decision":"deny","rule":"r1","reason":"no"}`},
		{"deny fallback", envelope.Blocking, "", "", "", `{"decision":"deny","reason":"Blocked by fas policy"}`},
		{"ask denies", envelope.Asking, "r2", "sure?", `{"command":"ls"}`, `{"decision":"deny","rule":"r2","reason":"sure?"}`},
		{"unknown category", 99, "", "", "", `{"decision":"deny","reason":"Blocked by fas policy"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := envelope.OutputEnvelope{Category: tc.category, RuleID: tc.rule, UserReason: tc.reason, AgentReason: "private"}
			if tc.update != "" {
				out.UpdatedInput = json.RawMessage(tc.update)
			}
			got, err := (adapter.Henia{}).RenderOutput(out, "PreToolUse")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
