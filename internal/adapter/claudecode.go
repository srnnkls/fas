// Package adapter — Claude Code implementation.
//
// The Claude Code hook protocol (PreToolUse) permits three permission
// decisions — deny, ask, allow — carried inside a hookSpecificOutput object.
// It has no payload-rewrite mechanism, which is why AllowsModify reports
// false and RenderOutput drops any UpdatedInput on the floor. See the
// design.md "Adapter Capabilities" section for the full contract.
package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/srnnkls/fas/internal/envelope"
)

// ClaudeCode implements Adapter for the Claude Code hook protocol.
type ClaudeCode struct{}

var _ Adapter = ClaudeCode{}

// Name returns the harness identifier used to select this adapter on the CLI.
func (ClaudeCode) Name() string {
	return "claude"
}

// AllowsModify reports whether this adapter can emit payload-rewrite effects.
// Claude Code's PreToolUse hook has no updatedInput channel; rule-loading
// layers must reject rules that emit modify actions when this is false.
func (ClaudeCode) AllowsModify() bool {
	return false
}

// ccInput mirrors the Claude Code PreToolUse hook payload subset the engine
// consumes. transcript_path is tolerated but intentionally not stored: the
// envelope has no slot for it.
type ccInput struct {
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	Prompt         string          `json:"prompt"`
	AgentType      string          `json:"agent_type"`
	SessionID      string          `json:"session_id"`
	CWD            string          `json:"cwd"`
	StopHookActive bool            `json:"stop_hook_active"`
	TranscriptPath string          `json:"transcript_path"`
}

// ParseInput normalizes a Claude Code hook JSON payload into envelope.Input.
// Non-object roots, malformed JSON, and missing hook_event_name are rejected.
func (ClaudeCode) ParseInput(raw json.RawMessage) (*envelope.Input, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		// Cheap structural check before json.Unmarshal so arrays, strings,
		// numbers, and null all fail with a uniform message rather than
		// silently decoding into a zero-valued struct.
		var probe any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("claudecode: malformed input: %w", err)
		}
		return nil, fmt.Errorf("claudecode: non-object input: got %T", probe)
	}

	var parsed ccInput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("claudecode: malformed input: %w", err)
	}
	if parsed.HookEventName == "" {
		return nil, errors.New("claudecode: missing required field hook_event_name")
	}

	return &envelope.Input{
		HookEventName:  parsed.HookEventName,
		ToolName:       parsed.ToolName,
		ToolInput:      parsed.ToolInput,
		ToolResponse:   parsed.ToolResponse,
		Prompt:         parsed.Prompt,
		AgentType:      parsed.AgentType,
		SessionID:      parsed.SessionID,
		CWD:            parsed.CWD,
		StopHookActive: parsed.StopHookActive,
	}, nil
}

// ccHookSpecificOutput is the ordered wire shape emitted inside
// hookSpecificOutput. Field order here fixes JSON key order for deterministic
// byte-level output.
type ccHookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
	AgentMessage             string `json:"agentMessage,omitempty"`
}

type ccResponse struct {
	HookSpecificOutput ccHookSpecificOutput `json:"hookSpecificOutput"`
}

// ccContextOutput is the wire shape for events that inject context but carry no
// permission decision (SubagentStart). Emitting permissionDecision there is off
// contract, so those events get a dedicated, decision-free shape.
type ccContextOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}

type ccContextResponse struct {
	HookSpecificOutput ccContextOutput `json:"hookSpecificOutput"`
}

// contextOnlyEvents are the hook events whose only output channel is
// additionalContext — they never carry a permissionDecision.
var contextOnlyEvents = map[string]bool{
	"SubagentStart": true,
}

// turnEndEvents are hook events fired when a turn (or subagent turn) is about
// to end. They never carry a permissionDecision; a fired continuation renders
// as the harness block-and-continue contract, otherwise the stop is allowed.
var turnEndEvents = map[string]bool{
	"Stop":         true,
	"SubagentStop": true,
}

// ccTurnEndResponse is the Stop/SubagentStop wire shape. A continuation sets
// the top-level decision/reason block contract; additionalContext rides along
// under hookSpecificOutput. All fields omitempty so a clean allow-stop with no
// context marshals to "{}".
type ccTurnEndResponse struct {
	Decision           string           `json:"decision,omitempty"`
	Reason             string           `json:"reason,omitempty"`
	HookSpecificOutput *ccContextOutput `json:"hookSpecificOutput,omitempty"`
}

// RenderOutput renders an OutputEnvelope as a Claude Code hook response. For
// PreToolUse the response carries a permission decision; context-only events
// (SubagentStart) carry just additionalContext; Stop/SubagentStop render the
// continuation block contract (no permissionDecision). UpdatedInput is silently
// dropped: CC has no consumer for it (see AllowsModify).
func (ClaudeCode) RenderOutput(out envelope.OutputEnvelope, hookEventName string) (json.RawMessage, error) {
	if turnEndEvents[hookEventName] {
		resp := ccTurnEndResponse{}
		if out.Continuation != nil {
			resp.Decision = "block"
			resp.Reason = out.Continuation.Reason
		}
		if out.AdditionalContext != "" {
			resp.HookSpecificOutput = &ccContextOutput{
				HookEventName:     hookEventName,
				AdditionalContext: out.AdditionalContext,
			}
		}
		return json.Marshal(resp)
	}

	if contextOnlyEvents[hookEventName] {
		return json.Marshal(ccContextResponse{HookSpecificOutput: ccContextOutput{
			HookEventName:     hookEventName,
			AdditionalContext: out.AdditionalContext,
		}})
	}

	decision := permissionDecisionFor(out.Category)

	hso := ccHookSpecificOutput{
		HookEventName:      hookEventName,
		PermissionDecision: decision,
		AdditionalContext:  out.AdditionalContext,
	}
	// Reason is optional on allow; required-or-omitted on deny/ask.
	if decision != "allow" || out.UserReason != "" {
		hso.PermissionDecisionReason = out.UserReason
	}
	// AgentMessage collapses to UserReason when identical to avoid duplicate
	// text in the two-channel message design.
	if out.AgentReason != "" && out.AgentReason != out.UserReason {
		hso.AgentMessage = out.AgentReason
	}

	return json.Marshal(ccResponse{HookSpecificOutput: hso})
}

func permissionDecisionFor(c envelope.Category) string {
	switch c {
	case envelope.Blocking:
		return "deny"
	case envelope.Asking:
		return "ask"
	case envelope.Allowing:
		return "allow"
	default:
		// Fail-closed on unexpected categories: prefer denying a hook over allowing it.
		return "deny"
	}
}
