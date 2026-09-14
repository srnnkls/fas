package adapter_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/srnnkls/fas/internal/adapter"
	"github.com/srnnkls/fas/internal/envelope"
)

func TestCodexParseInput(t *testing.T) {
	a := adapter.Codex{}
	if a.Name() != "codex" || a.AllowsModify() {
		t.Fatal("incorrect adapter capabilities")
	}
	raw := json.RawMessage(`{"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_input":{ "command": "patch" },"tool_response":{"ok":true},"prompt":"hello","agent_type":"worker","session_id":"s","cwd":"/tmp","turn_id":"t","tool_use_id":"u","model":"m","permission_mode":"default","agent_id":"a","transcript_path":"p"}`)
	got, err := a.ParseInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := &envelope.Input{HookEventName: "PreToolUse", ToolName: "apply_patch", ToolInput: json.RawMessage(`{ "command": "patch" }`), ToolResponse: json.RawMessage(`{"ok":true}`), Prompt: "hello", AgentType: "worker", SessionID: "s", CWD: "/tmp"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	for _, raw := range []string{`[]`, `"s"`, `42`, `null`, `{`, `{}`, `{"hook_event_name":42}`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := a.ParseInput(json.RawMessage(raw)); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestCodexRenderOutput(t *testing.T) {
	for _, tc := range []struct {
		event                 string
		category              envelope.Category
		context, reason, want string
	}{
		{"PreToolUse", envelope.Allowing, "", "", "{}"},
		{"PreToolUse", envelope.Blocking, "", "no", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"no"}}`},
		{"PreToolUse", envelope.Asking, "", "no", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"no"}}`},
		{"PreToolUse", envelope.Allowing, "ctx", "", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"ctx"}}`},
		{"UserPromptSubmit", envelope.Blocking, "ctx", "no", `{"decision":"block","reason":"no"}`},
		{"UserPromptSubmit", envelope.Asking, "ctx", "no", `{"decision":"block","reason":"no"}`},
	} {
		t.Run(tc.event+tc.want, func(t *testing.T) {
			got, err := (adapter.Codex{}).RenderOutput(envelope.OutputEnvelope{Category: tc.category, AdditionalContext: tc.context, UserReason: tc.reason, AgentReason: "private", UpdatedInput: json.RawMessage(`{"x":1}`)}, tc.event)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	for _, event := range []string{"PreToolUse", "UserPromptSubmit", "PostToolUse", "SubagentStart", "SessionStart", "Stop", "SubagentStop", "SessionEnd", "PreCompact", "PostCompact", "PermissionRequest", "unknown"} {
		deny, err := (adapter.Codex{}).RenderOutput(envelope.OutputEnvelope{Category: envelope.Blocking, AdditionalContext: "ctx"}, event)
		if err != nil {
			t.Fatal(err)
		}
		ask, err := (adapter.Codex{}).RenderOutput(envelope.OutputEnvelope{Category: envelope.Asking, AdditionalContext: "ctx"}, event)
		if err != nil {
			t.Fatal(err)
		}
		if string(ask) != string(deny) {
			t.Errorf("%s: ask differs from deny", event)
		}
	}
	for _, category := range []envelope.Category{envelope.Blocking, 99} {
		got, err := (adapter.Codex{}).RenderOutput(envelope.OutputEnvelope{Category: category, UserReason: " \t"}, "PreToolUse")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), `"permissionDecisionReason":"Blocked by fas policy"`) {
			t.Fatalf("missing fallback: %s", got)
		}
	}
}
