package parser_test

import (
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/srnnkls/fas/internal/parser"
)

// cloneInput deep-copies a map[string]any (one level of nested maps is enough
// for these fixtures). Tests use it to detect cross-call mutation.
func cloneInput(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch vv := v.(type) {
		case map[string]any:
			nested := make(map[string]any, len(vv))
			maps.Copy(nested, vv)
			out[k] = nested
		default:
			out[k] = v
		}
	}
	return out
}

func getToolInput(t *testing.T, in map[string]any) map[string]any {
	t.Helper()
	ti, ok := in["tool_input"].(map[string]any)
	if !ok {
		t.Fatalf("tool_input missing or wrong type: %#v", in["tool_input"])
	}
	return ti
}

func TestPreprocess_KnownTool_PopulatesParsed(t *testing.T) {
	input := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input": map[string]any{
			"command": "ls",
		},
		"session_id": "abc-123",
		"cwd":        "/home/user",
	}

	out, err := parser.Preprocess("Bash", input)
	if err != nil {
		t.Fatalf("Preprocess returned error: %v", err)
	}

	ti := getToolInput(t, out)
	parsed, ok := ti["parsed"].(parser.Parsed)
	if !ok {
		t.Fatalf("tool_input.parsed missing or wrong type: %#v (got type %T)", ti["parsed"], ti["parsed"])
	}

	if parsed.Actions == nil {
		t.Fatalf("parsed.Actions should be populated for known tool; got nil")
	}
}

func TestPreprocess_UnknownTool_PassesThroughUnchanged(t *testing.T) {
	input := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "MysteryTool",
		"tool_input": map[string]any{
			"foo": "bar",
		},
	}
	before := cloneInput(input)

	out, err := parser.Preprocess("MysteryTool", input)
	if err != nil {
		t.Fatalf("Preprocess returned error for unknown tool: %v", err)
	}

	if diff := cmp.Diff(before, out); diff != "" {
		t.Fatalf("unknown tool output diverged from input (-want +got):\n%s", diff)
	}

	ti := getToolInput(t, out)
	if _, present := ti["parsed"]; present {
		t.Fatalf("unknown tool must not add tool_input.parsed; got %#v", ti["parsed"])
	}
}

func TestPreprocess_NoNamespaceLeakage(t *testing.T) {
	input := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input": map[string]any{
			"command": "rm -rf /tmp/foo",
		},
		"session_id": "sid-xyz",
		"cwd":        "/work",
		"signals": map[string]any{
			"git_status": map[string]any{"ok": true},
		},
	}

	out, err := parser.Preprocess("Bash", input)
	if err != nil {
		t.Fatalf("Preprocess returned error: %v", err)
	}

	// Every top-level field other than tool_input must be byte-identical.
	for _, key := range []string{"hook_event_name", "tool_name", "session_id", "cwd"} {
		if diff := cmp.Diff(input[key], out[key]); diff != "" {
			t.Errorf("top-level field %q mutated (-want +got):\n%s", key, diff)
		}
	}
	if diff := cmp.Diff(input["signals"], out["signals"]); diff != "" {
		t.Errorf("signals mutated (-want +got):\n%s", diff)
	}

	// Inside tool_input, command must survive untouched; only `parsed` is new.
	ti := getToolInput(t, out)
	if got, want := ti["command"], "rm -rf /tmp/foo"; got != want {
		t.Errorf("tool_input.command mutated: got %q want %q", got, want)
	}
	for k := range ti {
		if k != "command" && k != "parsed" {
			t.Errorf("unexpected field inside tool_input: %q", k)
		}
	}
}

func TestPreprocess_Idempotent(t *testing.T) {
	input := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input": map[string]any{
			"command": "git branch -D feature",
		},
	}

	first, err := parser.Preprocess("Bash", input)
	if err != nil {
		t.Fatalf("first Preprocess returned error: %v", err)
	}
	second, err := parser.Preprocess("Bash", first)
	if err != nil {
		t.Fatalf("second Preprocess returned error: %v", err)
	}

	if diff := cmp.Diff(first, second); diff != "" {
		t.Fatalf("Preprocess is not idempotent (-first +second):\n%s", diff)
	}
}
