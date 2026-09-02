package envelope

import "encoding/json"

// Input is the internal, vendor-agnostic hook event after adapter normalization.
type Input struct {
	HookEventName        string                  `json:"hook_event_name"`
	ToolName             string                  `json:"tool_name,omitempty"`
	ToolInput            json.RawMessage         `json:"tool_input,omitempty"`
	ToolResponse         json.RawMessage         `json:"tool_response,omitempty"`
	ToolUseID            string                  `json:"tool_use_id,omitempty"`
	Prompt               string                  `json:"prompt,omitempty"`
	LastAssistantMessage string                  `json:"last_assistant_message,omitempty"`
	AgentID              string                  `json:"agent_id"`
	AgentType            string                  `json:"agent_type,omitempty"`
	SessionID            string                  `json:"session_id,omitempty"`
	PromptID             string                  `json:"prompt_id,omitempty"`
	TranscriptPath       string                  `json:"transcript_path,omitempty"`
	CWD                  string                  `json:"cwd,omitempty"`
	PermissionMode       string                  `json:"permission_mode,omitempty"`
	Effort               *Effort                 `json:"effort,omitempty"`
	Signals              map[string]SignalResult `json:"signals,omitempty"`
}

// Effort is the reasoning-effort level in force when the hook fired.
type Effort struct {
	Level string `json:"level,omitempty"`
}

// SignalResult is the enrichment payload attached by a Wasm signal module.
type SignalResult struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data,omitempty"`
	Err  string          `json:"err,omitempty"`
}
