package adapter_test

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/srnnkls/fas/internal/adapter"
	"github.com/srnnkls/fas/internal/envelope"
)

func TestPiParseInput(t *testing.T) {
	a := adapter.Pi{}
	if a.Name() != "pi" || !a.AllowsModify() {
		t.Fatal("incorrect adapter capabilities")
	}
	raw := json.RawMessage(`{"hook_event_name":"PostToolUse","tool_name":"bash","tool_input":{"command":"ls"},"tool_response":{"ok":true},"tool_call_id":"c1","session_id":"s","cwd":"/tmp","extra":1}`)
	got, err := a.ParseInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := &envelope.Input{HookEventName: "PostToolUse", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`), ToolResponse: json.RawMessage(`{"ok":true}`), ToolUseID: "c1", SessionID: "s", CWD: "/tmp"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	for host, catalog := range map[string]string{"bash": "Bash", "read": "Read", "write": "Write", "edit": "Edit", "grep": "Grep", "find": "Glob", "glob": "Glob", "ls": "ls", "mcp_tool": "mcp_tool", "Bash": "Bash"} {
		got, err := a.ParseInput(json.RawMessage(`{"hook_event_name":"PreToolUse","tool_name":"` + host + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		if got.ToolName != catalog {
			t.Errorf("%s: got %s, want %s", host, got.ToolName, catalog)
		}
	}
	for _, raw := range []string{`[]`, `"s"`, `42`, `null`, `{`, `{}`, `{"hook_event_name":42}`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := a.ParseInput(json.RawMessage(raw)); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestPiRenderOutput(t *testing.T) {
	for _, tc := range []struct {
		name, event    string
		category       envelope.Category
		reason, update string
		want           string
	}{
		{"allow", "PreToolUse", envelope.Allowing, "", "", `{}`},
		{"allow modify", "PreToolUse", envelope.Allowing, "", `{"command":"ls"}`, `{"decision":"allow","updated_input":{"command":"ls"}}`},
		{"ask", "PreToolUse", envelope.Asking, "sure?", "", `{"decision":"ask","reason":"sure?"}`},
		{"ask modify", "PreToolUse", envelope.Asking, "sure?", `{"command":"ls"}`, `{"decision":"ask","reason":"sure?","updated_input":{"command":"ls"}}`},
		{"ask fallback", "PreToolUse", envelope.Asking, " \t", "", `{"decision":"ask","reason":"Confirmation required by fas policy"}`},
		{"deny", "PreToolUse", envelope.Blocking, "no", "", `{"decision":"deny","reason":"no"}`},
		{"deny fallback", "PreToolUse", envelope.Blocking, "", "", `{"decision":"deny","reason":"Blocked by fas policy"}`},
		{"unknown category", "PreToolUse", 99, "", "", `{"decision":"deny","reason":"Blocked by fas policy"}`},
		{"post context", "PostToolUse", envelope.Blocking, "no", `{"command":"ls"}`, `{"additional_context":"ctx"}`},
		{"other event", "Stop", envelope.Blocking, "no", "", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := envelope.OutputEnvelope{Category: tc.category, UserReason: tc.reason, AgentReason: "private", AdditionalContext: "ctx"}
			if tc.update != "" {
				out.UpdatedInput = json.RawMessage(tc.update)
			}
			if tc.event == "PreToolUse" {
				out.AdditionalContext = ""
			}
			got, err := (adapter.Pi{}).RenderOutput(out, tc.event)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
