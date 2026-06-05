package envelope

import "encoding/json"

// Input is the internal, vendor-agnostic hook event after adapter normalization.
type Input struct {
	HookEventName string                  `json:"hook_event_name"`
	ToolName      string                  `json:"tool_name,omitempty"`
	ToolInput     json.RawMessage         `json:"tool_input,omitempty"`
	ToolResponse  json.RawMessage         `json:"tool_response,omitempty"`
	Prompt        string                  `json:"prompt,omitempty"`
	AgentType     string                  `json:"agent_type,omitempty"`
	SessionID     string                  `json:"session_id,omitempty"`
	CWD           string                  `json:"cwd,omitempty"`
	Signals       map[string]SignalResult `json:"signals,omitempty"`
}

// SignalResult is the enrichment payload attached by a Wasm signal module.
type SignalResult struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data,omitempty"`
	Err  string          `json:"err,omitempty"`
}
