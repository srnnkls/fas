package adapter_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/srnnkls/quae/internal/adapter"
	"github.com/srnnkls/quae/internal/envelope"
)

// ccResponse mirrors the Claude Code PreToolUse hook response shape. The
// engine's RenderOutput output is unmarshaled into this struct and asserted
// field-by-field, so key ordering in the emitted JSON does not matter.
type ccResponse struct {
	HookSpecificOutput ccHookSpecificOutput `json:"hookSpecificOutput"`
}

type ccHookSpecificOutput struct {
	HookEventName            string          `json:"hookEventName"`
	PermissionDecision       string          `json:"permissionDecision"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string          `json:"additionalContext,omitempty"`
	AgentMessage             string          `json:"agentMessage,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
}

func newClaude() adapter.Adapter {
	return adapter.ClaudeCode{}
}

func TestClaudeCode_Name_ReturnsClaude(t *testing.T) {
	a := newClaude()
	if got, want := a.Name(), "claude"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestClaudeCode_AllowsModify_ReturnsFalse(t *testing.T) {
	a := newClaude()
	if a.AllowsModify() {
		t.Error("AllowsModify() = true, want false (CC has no payload-rewrite mechanism)")
	}
}

func TestClaudeCode_ParseInput_ValidPreToolUseBash(t *testing.T) {
	raw := json.RawMessage(`{
	  "hook_event_name": "PreToolUse",
	  "tool_name": "Bash",
	  "tool_input": {"command": "ls -la"},
	  "session_id": "sess-abc",
	  "cwd": "/tmp/project",
	  "transcript_path": "/tmp/transcript.jsonl"
	}`)

	in, err := newClaude().ParseInput(raw)
	if err != nil {
		t.Fatalf("ParseInput: unexpected error: %v", err)
	}
	if in == nil {
		t.Fatal("ParseInput: returned nil Input")
	}
	if got, want := in.HookEventName, "PreToolUse"; got != want {
		t.Errorf("HookEventName = %q, want %q", got, want)
	}
	if got, want := in.ToolName, "Bash"; got != want {
		t.Errorf("ToolName = %q, want %q", got, want)
	}
	if got, want := in.SessionID, "sess-abc"; got != want {
		t.Errorf("SessionID = %q, want %q", got, want)
	}
	if got, want := in.CWD, "/tmp/project"; got != want {
		t.Errorf("CWD = %q, want %q", got, want)
	}
	if len(in.ToolInput) == 0 {
		t.Fatal("ToolInput is empty; expected preserved raw object")
	}
}

func TestClaudeCode_ParseInput_MissingHookEventName_Errors(t *testing.T) {
	raw := json.RawMessage(`{"tool_name": "Bash", "tool_input": {"command": "ls"}}`)
	if _, err := newClaude().ParseInput(raw); err == nil {
		t.Fatal("ParseInput: expected error on missing hook_event_name, got nil")
	}
}

func TestClaudeCode_ParseInput_MalformedJSON_Errors(t *testing.T) {
	raw := json.RawMessage(`{"hook_event_name": "PreToolUse",`)
	_, err := newClaude().ParseInput(raw)
	if err == nil {
		t.Fatal("ParseInput: expected error on malformed JSON, got nil")
	}
	// Wrapped error should either expose a *json.SyntaxError through the
	// unwrap chain or mention "json" in its message.
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) && !strings.Contains(strings.ToLower(err.Error()), "json") {
		t.Errorf("ParseInput: error %q should wrap a JSON decoding error", err)
	}
}

func TestClaudeCode_ParseInput_NonObjectRoot_Errors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"array root", `["PreToolUse"]`},
		{"string root", `"PreToolUse"`},
		{"number root", `42`},
		{"null root", `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newClaude().ParseInput(json.RawMessage(tc.raw)); err == nil {
				t.Fatalf("ParseInput(%s): expected error, got nil", tc.raw)
			}
		})
	}
}

func TestClaudeCode_ParseInput_ToolInputPreservedAsRaw(t *testing.T) {
	// The inner tool_input contains nested objects and arrays. The adapter
	// must preserve these bytes structurally so downstream parsers see the
	// exact payload CC sent.
	raw := json.RawMessage(`{
	  "hook_event_name": "PreToolUse",
	  "tool_name": "Bash",
	  "tool_input": {"command": "rm -rf /", "extras": {"nested": [1, 2, 3]}}
	}`)

	in, err := newClaude().ParseInput(raw)
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if len(in.ToolInput) == 0 {
		t.Fatal("ToolInput is empty; expected preserved raw object")
	}

	// Structural equality: re-decoding ToolInput must yield the same value
	// we put in. We go through an intermediate map compare since whitespace
	// may be normalized.
	var got, want map[string]any
	if err := json.Unmarshal(in.ToolInput, &got); err != nil {
		t.Fatalf("ToolInput is not valid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"command":"rm -rf /","extras":{"nested":[1,2,3]}}`), &want); err != nil {
		t.Fatalf("want fixture invalid: %v", err)
	}
	if !jsonEqual(got, want) {
		t.Errorf("ToolInput mismatch:\n got  %s\n want %s", in.ToolInput, `{"command":"rm -rf /","extras":{"nested":[1,2,3]}}`)
	}
}

func TestClaudeCode_ParseInput_OptionalFieldsDefault(t *testing.T) {
	raw := json.RawMessage(`{"hook_event_name": "SessionStart"}`)
	in, err := newClaude().ParseInput(raw)
	if err != nil {
		t.Fatalf("ParseInput: unexpected error: %v", err)
	}
	if in == nil {
		t.Fatal("ParseInput: returned nil Input")
	}
	if in.HookEventName != "SessionStart" {
		t.Errorf("HookEventName = %q, want %q", in.HookEventName, "SessionStart")
	}
	if in.ToolName != "" {
		t.Errorf("ToolName = %q, want empty", in.ToolName)
	}
	if in.SessionID != "" {
		t.Errorf("SessionID = %q, want empty", in.SessionID)
	}
	if in.CWD != "" {
		t.Errorf("CWD = %q, want empty", in.CWD)
	}
	if len(in.ToolInput) != 0 {
		t.Errorf("ToolInput = %s, want empty", in.ToolInput)
	}
}

func TestClaudeCode_RenderOutput_Blocking_EmitsDenyDecision(t *testing.T) {
	out := envelope.OutputEnvelope{
		Category:   envelope.Blocking,
		UserReason: "Destructive rm forbidden on system paths",
	}
	resp := renderAndDecode(t, out, "PreToolUse")
	if got, want := resp.HookSpecificOutput.PermissionDecision, "deny"; got != want {
		t.Errorf("permissionDecision = %q, want %q", got, want)
	}
	if got, want := resp.HookSpecificOutput.PermissionDecisionReason, "Destructive rm forbidden on system paths"; got != want {
		t.Errorf("permissionDecisionReason = %q, want %q", got, want)
	}
}

func TestClaudeCode_RenderOutput_Asking_EmitsAskDecision(t *testing.T) {
	out := envelope.OutputEnvelope{
		Category:   envelope.Asking,
		UserReason: "Please confirm network access",
	}
	resp := renderAndDecode(t, out, "PreToolUse")
	if got, want := resp.HookSpecificOutput.PermissionDecision, "ask"; got != want {
		t.Errorf("permissionDecision = %q, want %q", got, want)
	}
	if got, want := resp.HookSpecificOutput.PermissionDecisionReason, "Please confirm network access"; got != want {
		t.Errorf("permissionDecisionReason = %q, want %q", got, want)
	}
}

func TestClaudeCode_RenderOutput_Allowing_EmitsAllowDecision(t *testing.T) {
	out := envelope.OutputEnvelope{Category: envelope.Allowing}
	resp := renderAndDecode(t, out, "PreToolUse")
	if got, want := resp.HookSpecificOutput.PermissionDecision, "allow"; got != want {
		t.Errorf("permissionDecision = %q, want %q", got, want)
	}
	if resp.HookSpecificOutput.PermissionDecisionReason != "" {
		t.Errorf("permissionDecisionReason = %q, want empty for Allowing", resp.HookSpecificOutput.PermissionDecisionReason)
	}
}

func TestClaudeCode_RenderOutput_HookEventName_EchoedFromArg(t *testing.T) {
	cases := []string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "SessionStart", "Stop"}
	for _, event := range cases {
		t.Run(event, func(t *testing.T) {
			out := envelope.OutputEnvelope{Category: envelope.Allowing}
			resp := renderAndDecode(t, out, event)
			if got := resp.HookSpecificOutput.HookEventName; got != event {
				t.Errorf("hookEventName = %q, want %q", got, event)
			}
		})
	}
}

func TestClaudeCode_RenderOutput_BlockingWithUpdatedInput_IgnoresUpdatedInput(t *testing.T) {
	out := envelope.OutputEnvelope{
		Category:     envelope.Blocking,
		UserReason:   "Nope",
		UpdatedInput: json.RawMessage(`{"command":"ls"}`),
	}
	raw, err := newClaude().RenderOutput(out, "PreToolUse")
	if err != nil {
		t.Fatalf("RenderOutput: %v", err)
	}

	// Structural assertion: the emitted JSON must not contain an updatedInput
	// field anywhere in hookSpecificOutput — CC cannot consume it and the
	// envelope sender should not leak it.
	var resp ccResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("emitted JSON not parseable: %v", err)
	}
	if len(resp.HookSpecificOutput.UpdatedInput) != 0 {
		t.Errorf("updatedInput leaked: %s", resp.HookSpecificOutput.UpdatedInput)
	}
	// Belt-and-braces: check there is no "updatedInput" key textually either.
	if bytes.Contains(raw, []byte("updatedInput")) {
		t.Errorf("emitted JSON unexpectedly mentions updatedInput: %s", raw)
	}
}

func TestClaudeCode_RenderOutput_AdditionalContext_Present_EmitsKey(t *testing.T) {
	out := envelope.OutputEnvelope{
		Category:          envelope.Allowing,
		AdditionalContext: "Repo is clean.",
	}
	resp := renderAndDecode(t, out, "PreToolUse")
	if got, want := resp.HookSpecificOutput.AdditionalContext, "Repo is clean."; got != want {
		t.Errorf("additionalContext = %q, want %q", got, want)
	}
}

func TestClaudeCode_RenderOutput_AdditionalContext_Empty_OmitsKey(t *testing.T) {
	out := envelope.OutputEnvelope{Category: envelope.Allowing}
	raw, err := newClaude().RenderOutput(out, "PreToolUse")
	if err != nil {
		t.Fatalf("RenderOutput: %v", err)
	}
	if bytes.Contains(raw, []byte("additionalContext")) {
		t.Errorf("additionalContext key should be omitted when empty; got %s", raw)
	}
}

func TestClaudeCode_RenderOutput_UserReason_MapsToPermissionDecisionReason(t *testing.T) {
	cases := []struct {
		name string
		cat  envelope.Category
	}{
		{"Blocking", envelope.Blocking},
		{"Asking", envelope.Asking},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := envelope.OutputEnvelope{
				Category:   tc.cat,
				UserReason: "user-facing explanation",
			}
			resp := renderAndDecode(t, out, "PreToolUse")
			if got, want := resp.HookSpecificOutput.PermissionDecisionReason, "user-facing explanation"; got != want {
				t.Errorf("permissionDecisionReason = %q, want %q", got, want)
			}
		})
	}
}

func TestClaudeCode_RenderOutput_AgentReason_MapsToAgentMessage(t *testing.T) {
	out := envelope.OutputEnvelope{
		Category:    envelope.Blocking,
		UserReason:  "Blocked: see policy",
		AgentReason: "detailed rationale for the agent",
	}
	resp := renderAndDecode(t, out, "PreToolUse")
	if got, want := resp.HookSpecificOutput.AgentMessage, "detailed rationale for the agent"; got != want {
		t.Errorf("agentMessage = %q, want %q", got, want)
	}
	// UserReason must remain in its own slot; agent reason must not clobber it.
	if got, want := resp.HookSpecificOutput.PermissionDecisionReason, "Blocked: see policy"; got != want {
		t.Errorf("permissionDecisionReason = %q, want %q", got, want)
	}
}

func TestClaudeCode_RenderOutput_AgentReason_EqualToUserReason_OmitsAgentMessage(t *testing.T) {
	// Per spec: AgentReason is rendered only when non-empty and distinct
	// from UserReason. Identical values should collapse to a single slot.
	out := envelope.OutputEnvelope{
		Category:    envelope.Blocking,
		UserReason:  "same message",
		AgentReason: "same message",
	}
	raw, err := newClaude().RenderOutput(out, "PreToolUse")
	if err != nil {
		t.Fatalf("RenderOutput: %v", err)
	}
	if bytes.Contains(raw, []byte("agentMessage")) {
		t.Errorf("agentMessage should be omitted when equal to userReason; got %s", raw)
	}
}

func TestClaudeCode_RenderOutput_DeterministicOutput_ByteForByte(t *testing.T) {
	out := envelope.OutputEnvelope{
		Category:          envelope.Blocking,
		UserReason:        "nope",
		AgentReason:       "agent detail",
		AdditionalContext: "context",
	}
	a := newClaude()
	first, err := a.RenderOutput(out, "PreToolUse")
	if err != nil {
		t.Fatalf("first RenderOutput: %v", err)
	}
	second, err := a.RenderOutput(out, "PreToolUse")
	if err != nil {
		t.Fatalf("second RenderOutput: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("non-deterministic RenderOutput output:\n first:  %s\n second: %s", first, second)
	}
}

// --- helpers ---------------------------------------------------------------

func renderAndDecode(t *testing.T, out envelope.OutputEnvelope, event string) ccResponse {
	t.Helper()
	raw, err := newClaude().RenderOutput(out, event)
	if err != nil {
		t.Fatalf("RenderOutput: %v", err)
	}
	var resp ccResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("emitted JSON not parseable: %v (raw=%s)", err, raw)
	}
	return resp
}

// jsonEqual does a deep structural compare over already-decoded map values.
func jsonEqual(a, b map[string]any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return bytes.Equal(aj, bj)
}
