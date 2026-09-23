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

// Pi implements the protocol spoken by the pi/omp extension in extensions/fas.
type Pi struct{}

var _ Adapter = Pi{}

func (Pi) Name() string { return "pi" }

func (Pi) AllowsModify() bool { return true }

var piToolNames = map[string]string{
	"bash":  "Bash",
	"read":  "Read",
	"write": "Write",
	"edit":  "Edit",
	"grep":  "Grep",
	"find":  "Glob",
	"glob":  "Glob",
}

func (Pi) ParseInput(raw json.RawMessage) (*envelope.Input, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		var probe any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("pi: malformed input: %w", err)
		}
		return nil, fmt.Errorf("pi: non-object input: got %T", probe)
	}
	var input struct {
		HookEventName string          `json:"hook_event_name"`
		ToolName      string          `json:"tool_name"`
		ToolInput     json.RawMessage `json:"tool_input"`
		ToolResponse  json.RawMessage `json:"tool_response"`
		ToolCallID    string          `json:"tool_call_id"`
		SessionID     string          `json:"session_id"`
		CWD           string          `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("pi: malformed input: %w", err)
	}
	if input.HookEventName == "" {
		return nil, errors.New("pi: missing required field hook_event_name")
	}
	return &envelope.Input{
		HookEventName: input.HookEventName,
		ToolName:      cmp.Or(piToolNames[input.ToolName], input.ToolName),
		ToolInput:     input.ToolInput,
		ToolResponse:  input.ToolResponse,
		ToolUseID:     input.ToolCallID,
		SessionID:     input.SessionID,
		CWD:           input.CWD,
	}, nil
}

type piResponse struct {
	Decision          string          `json:"decision,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	UpdatedInput      json.RawMessage `json:"updated_input,omitempty"`
	AdditionalContext string          `json:"additional_context,omitempty"`
}

func (Pi) RenderOutput(out envelope.OutputEnvelope, event string) (json.RawMessage, error) {
	var resp piResponse
	switch event {
	case "PreToolUse":
		switch out.Category {
		case envelope.Allowing:
			if len(out.UpdatedInput) > 0 {
				resp.Decision = "allow"
				resp.UpdatedInput = out.UpdatedInput
			}
		case envelope.Asking:
			resp.Decision = "ask"
			resp.Reason = cmp.Or(strings.TrimSpace(out.UserReason), "Confirmation required by fas policy")
			resp.UpdatedInput = out.UpdatedInput
		default:
			resp.Decision = "deny"
			resp.Reason = cmp.Or(strings.TrimSpace(out.UserReason), "Blocked by fas policy")
		}
	case "PostToolUse":
		resp.AdditionalContext = out.AdditionalContext
	}
	return json.Marshal(resp)
}
