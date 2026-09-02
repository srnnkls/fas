package envelope

import "encoding/json"

// Input is the internal, vendor-agnostic hook event after adapter normalization.
//
// StopHookActive and DurationMS are pointers so a present-and-zero value
// (`false`, `0`) survives omitempty and stays matchable; nil means the harness
// did not send the field. BackgroundTasks and SessionCrons stay raw for the
// same reason — an empty array is a meaningful "nothing in flight" and must not
// collapse to absence.
type Input struct {
	HookEventName        string                  `json:"hook_event_name"`
	ToolName             string                  `json:"tool_name,omitempty"`
	ToolInput            json.RawMessage         `json:"tool_input,omitempty"`
	ToolResponse         json.RawMessage         `json:"tool_response,omitempty"`
	ToolUseID            string                  `json:"tool_use_id,omitempty"`
	DurationMS           *int64                  `json:"duration_ms,omitempty"`
	Prompt               string                  `json:"prompt,omitempty"`
	Message              string                  `json:"message,omitempty"`
	Title                string                  `json:"title,omitempty"`
	NotificationType     string                  `json:"notification_type,omitempty"`
	LastAssistantMessage string                  `json:"last_assistant_message,omitempty"`
	StopHookActive       *bool                   `json:"stop_hook_active,omitempty"`
	BackgroundTasks      json.RawMessage         `json:"background_tasks,omitempty"`
	SessionCrons         json.RawMessage         `json:"session_crons,omitempty"`
	AgentID              string                  `json:"agent_id"`
	AgentType            string                  `json:"agent_type,omitempty"`
	AgentTranscriptPath  string                  `json:"agent_transcript_path,omitempty"`
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
