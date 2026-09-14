package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/envelope"
)

type codexAllowedKeys struct{ top, specific string }

// Literal oracle transcribed from Codex 0.148.0's command.output schemas.
// Schema admission and runtime restrictions are checked separately. In
// particular PermissionRequest admits output even though fas doesn't emit it.
var codexWireKeys = map[string]codexAllowedKeys{
	"PreToolUse":        {"continue decision hookSpecificOutput reason stopReason suppressOutput systemMessage", "additionalContext hookEventName permissionDecision permissionDecisionReason updatedInput"},
	"PostToolUse":       {"continue decision hookSpecificOutput reason stopReason suppressOutput systemMessage", "additionalContext hookEventName updatedMCPToolOutput"},
	"UserPromptSubmit":  {"continue decision hookSpecificOutput reason stopReason suppressOutput systemMessage", "additionalContext hookEventName"},
	"SubagentStart":     {"continue hookSpecificOutput stopReason suppressOutput systemMessage", "additionalContext hookEventName"},
	"SessionStart":      {"continue hookSpecificOutput stopReason suppressOutput systemMessage", "additionalContext hookEventName"},
	"PermissionRequest": {"continue hookSpecificOutput stopReason suppressOutput systemMessage", "decision hookEventName"},
	"Stop":              {"continue decision reason stopReason suppressOutput systemMessage", ""},
	"SubagentStop":      {"continue decision reason stopReason suppressOutput systemMessage", ""},
	"PreCompact":        {"continue stopReason suppressOutput systemMessage", ""},
	"PostCompact":       {"continue stopReason suppressOutput systemMessage", ""},
	"SessionEnd":        {"", ""}, // No output schema is registered.
}

func codexWireError(event string, raw []byte) error {
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		return err
	}
	keys := codexWireKeys[event]
	for key := range output {
		if !slices.Contains(strings.Fields(keys.top), key) {
			return fmt.Errorf("unexpected top-level key %s", key)
		}
	}
	specific, exists := output["hookSpecificOutput"]
	if !exists {
		return nil
	}
	hso, ok := specific.(map[string]any)
	if !ok {
		return errors.New("invalid hookSpecificOutput")
	}
	for key := range hso {
		if !slices.Contains(strings.Fields(keys.specific), key) {
			return fmt.Errorf("unexpected hook key %s", key)
		}
	}
	if hso["hookEventName"] != event {
		return errors.New("wrong event")
	}
	decision, hasDecision := hso["permissionDecision"]
	reason, hasReason := hso["permissionDecisionReason"]
	if hasDecision && decision != "deny" {
		return errors.New("unsupported permission decision")
	}
	if hasReason && !hasDecision {
		return errors.New("orphan reason")
	}
	if hasDecision {
		if s, ok := reason.(string); !ok || strings.TrimSpace(s) == "" {
			return errors.New("empty deny reason")
		}
	}
	if _, ok := hso["updatedInput"]; ok {
		return errors.New("unreachable updatedInput")
	}
	return nil
}

func TestCodexOutputConformance(t *testing.T) {
	for _, events := range []map[string]bool{codexPermissionEvents, codexContextEvents} {
		for event := range events {
			if _, ok := codexWireKeys[event]; !ok {
				t.Fatalf("event %s has no wire oracle", event)
			}
		}
	}
	for event := range codexWireKeys {
		for _, category := range []envelope.Category{envelope.Blocking, envelope.Asking, envelope.Allowing, 99} {
			for _, context := range []string{"", "context"} {
				t.Run(fmt.Sprintf("%s/%d/%s", event, category, context), func(t *testing.T) {
					raw, err := (Codex{}).RenderOutput(envelope.OutputEnvelope{Category: category, AdditionalContext: context, AgentReason: "agent", UpdatedInput: json.RawMessage(`{"x":1}`)}, event)
					if err != nil {
						t.Fatal(err)
					}
					if err := codexWireError(event, raw); err != nil {
						t.Fatalf("%s: %v", raw, err)
					}
					switch event {
					case "Stop", "SubagentStop", "SessionEnd", "PreCompact", "PostCompact", "PermissionRequest":
						if string(raw) != "{}" {
							t.Fatalf("unsupported event emitted %s", raw)
						}
					case "PostToolUse", "SubagentStart", "SessionStart":
						var output struct {
							HookSpecificOutput struct{ HookEventName, AdditionalContext string }
						}
						if err := json.Unmarshal(raw, &output); err != nil {
							t.Fatal(err)
						}
						if output.HookSpecificOutput.HookEventName != event || output.HookSpecificOutput.AdditionalContext != context {
							t.Fatalf("lost context: %s", raw)
						}
					}
				})
			}
		}
	}
}

func TestCodexConformanceFalsifiers(t *testing.T) {
	for _, tc := range []struct{ event, raw string }{
		{"PreToolUse", `{"extra":true}`},
		{"PreToolUse", `{"agentMessage":"x"}`},
		{"PreToolUse", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","agentMessage":"x"}}`},
		{"PreToolUse", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`},
		{"PreToolUse", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask"}}`},
		{"PreToolUse", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecisionReason":"x"}}`},
		{"PreToolUse", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":""}}`},
		{"Stop", `{"hookSpecificOutput":{"hookEventName":"Stop"}}`},
		{"PreToolUse", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{}}}`},
	} {
		if err := codexWireError(tc.event, []byte(tc.raw)); err == nil {
			t.Errorf("oracle accepted %s", tc.raw)
		}
	}
	if err := codexWireError("PermissionRequest", []byte(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny"}}}`)); err != nil {
		t.Fatalf("oracle rejects wire-legal PermissionRequest: %v", err)
	}
}
