package config_test

import (
	"strings"
	"testing"

	"github.com/srnnkls/quae/internal/config"
)

func TestValidateInput_AcceptsWellFormedPreToolUse(t *testing.T) {
	raw := []byte(`{
		"hook_event_name": "PreToolUse",
		"tool_name": "Bash",
		"tool_input": {"command": "ls"}
	}`)
	if err := config.ValidateInput(raw); err != nil {
		t.Fatalf("expected valid input, got error: %v", err)
	}
}

func TestValidateInput_RejectsMissingHookEventName(t *testing.T) {
	raw := []byte(`{
		"tool_name": "Bash",
		"tool_input": {"command": "ls"}
	}`)
	err := config.ValidateInput(raw)
	if err == nil {
		t.Fatalf("expected error for missing hook_event_name, got nil")
	}
	// The error should name the missing required field so callers can
	// surface an actionable message.
	if !strings.Contains(err.Error(), "hook_event_name") {
		t.Fatalf("error should mention missing 'hook_event_name', got: %v", err)
	}
}

func TestValidateInput_RejectsWrongTypeForHookEventName(t *testing.T) {
	// hook_event_name must be a string per #Input.
	raw := []byte(`{
		"hook_event_name": 42,
		"tool_name": "Bash"
	}`)
	err := config.ValidateInput(raw)
	if err == nil {
		t.Fatalf("expected type mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "hook_event_name") {
		t.Fatalf("error should mention 'hook_event_name', got: %v", err)
	}
}

func TestValidateInput_AcceptsOptionalFieldsAbsent(t *testing.T) {
	// Only the required field is present; everything else is optional.
	raw := []byte(`{"hook_event_name": "Stop"}`)
	if err := config.ValidateInput(raw); err != nil {
		t.Fatalf("expected valid minimal input, got error: %v", err)
	}
}

func TestValidateInput_RejectsInvalidJSON(t *testing.T) {
	raw := []byte(`{not valid json`)
	if err := config.ValidateInput(raw); err == nil {
		t.Fatalf("expected error for malformed JSON, got nil")
	}
}
