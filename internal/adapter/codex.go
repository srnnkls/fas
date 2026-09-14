package adapter

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/srnnkls/fas/internal/envelope"
)

// Codex implements the Codex CLI hook protocol verified against 0.148.0.
type Codex struct{}

var _ Adapter = Codex{}

func (Codex) Name() string { return "codex" }

// AllowsModify is false: Codex requires allow for updatedInput but rejects allow itself.
func (Codex) AllowsModify() bool { return false }

func (Codex) ParseInput(raw json.RawMessage) (*envelope.Input, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		var probe any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("codex: malformed input: %w", err)
		}
		return nil, fmt.Errorf("codex: non-object input: got %T", probe)
	}
	// Codex's rule-visible fields are independent of Claude's evolving schema.
	var input struct {
		HookEventName string          `json:"hook_event_name"`
		ToolName      string          `json:"tool_name"`
		ToolInput     json.RawMessage `json:"tool_input"`
		ToolResponse  json.RawMessage `json:"tool_response"`
		Prompt        string          `json:"prompt"`
		AgentType     string          `json:"agent_type"`
		SessionID     string          `json:"session_id"`
		CWD           string          `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("codex: malformed input: %w", err)
	}
	if input.HookEventName == "" {
		return nil, errors.New("codex: missing required field hook_event_name")
	}
	return &envelope.Input{HookEventName: input.HookEventName, ToolName: input.ToolName, ToolInput: input.ToolInput, ToolResponse: input.ToolResponse, Prompt: input.Prompt, AgentType: input.AgentType, SessionID: input.SessionID, CWD: input.CWD}, nil
}

type codexHookOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

type codexResponse struct {
	Decision           string           `json:"decision,omitempty"`
	Reason             string           `json:"reason,omitempty"`
	HookSpecificOutput *codexHookOutput `json:"hookSpecificOutput,omitempty"`
}

var codexPermissionEvents = map[string]bool{"PreToolUse": true}
var codexContextEvents = map[string]bool{
	"PostToolUse": true, "UserPromptSubmit": true, "SubagentStart": true, "SessionStart": true,
}

func (Codex) RenderOutput(out envelope.OutputEnvelope, event string) (json.RawMessage, error) {
	reason := cmp.Or(strings.TrimSpace(out.UserReason), "Blocked by fas policy")
	if event == "UserPromptSubmit" && out.Category != envelope.Allowing {
		return json.Marshal(codexResponse{Decision: "block", Reason: reason})
	}
	hso := codexHookOutput{HookEventName: event, AdditionalContext: out.AdditionalContext}
	switch {
	case codexPermissionEvents[event]:
		// Asking has no human escalation channel; unknown categories also fail closed.
		if out.Category != envelope.Allowing {
			hso.PermissionDecision = "deny"
			hso.PermissionDecisionReason = reason
		} else if out.AdditionalContext == "" {
			return json.RawMessage("{}"), nil
		}
	case codexContextEvents[event]:
	default:
		return json.RawMessage("{}"), nil
	}
	return json.Marshal(codexResponse{HookSpecificOutput: &hso})
}
