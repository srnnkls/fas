package parser_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/srnnkls/fas/internal/parser"
)

func TestParseApplyPatch(t *testing.T) {
	for _, tc := range []struct {
		name, patch      string
		targets, actions []string
	}{
		{"CRLF", "*** Begin Patch\r\n*** Delete File: old\r\n*** End Patch\r\n", []string{"old"}, []string{"delete"}},
		{"mixed", "*** Begin Patch\n*** Add File: note.txt\n+hello\n*** Update File: /etc/hosts\n@@\n-old\n+new\n*** Delete File: old.txt\n*** End Patch", []string{"note.txt", "/etc/hosts", "old.txt"}, []string{"add", "update", "delete"}},
		{"move and duplicate", "*** Begin Patch\n*** Update File: source name\n*** Move to: dest name\n@@\n-old\n+new\n*** Delete File: source name\n*** End Patch", []string{"source name", "dest name"}, []string{"update", "move", "delete"}},
		{"header in content", "*** Begin Patch\n*** Add File: safe\n+*** Delete File: /etc/hosts\n*** End Patch", []string{"safe"}, []string{"add"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parser.ParseApplyPatch(tc.patch)
			if got.Attributes["parse_error"] != nil {
				t.Fatal(got.Attributes)
			}
			if diff := cmp.Diff(tc.targets, got.Targets); diff != "" {
				t.Fatal(diff)
			}
			if diff := cmp.Diff(tc.actions, got.Actions); diff != "" {
				t.Fatal(diff)
			}
		})
	}
	for _, patch := range []string{"", "*** Add File: x", "*** Begin Patch\n*** Add File:\n*** End Patch", "*** Begin Patch\n*** Add File: x", "*** Begin Patch\n*** Move to: x\n*** End Patch", "*** Begin Patch\n*** Add File: x\nbad\n*** End Patch"} {
		if got := parser.ParseApplyPatch(patch); got.Attributes["parse_error"] == nil {
			t.Errorf("accepted malformed patch %q", patch)
		}
	}
}

func TestApplyPatchPreprocess(t *testing.T) {
	for _, command := range []string{"broken", "*** Begin Patch\n*** Delete File: old\n*** End Patch"} {
		input := map[string]any{"tool_name": "apply_patch", "other": "preserved", "tool_input": map[string]any{"command": command, "extra": 42}}
		got, err := parser.Preprocess("apply_patch", input)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := parser.Preprocess("apply_patch", got)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(got, twice); diff != "" {
			t.Fatal(diff)
		}
		ti := got["tool_input"].(map[string]any)
		if _, ok := input["tool_input"].(map[string]any)["parsed"]; ok {
			t.Fatal("mutated original")
		}
		if _, ok := ti["parsed"].(parser.Parsed); !ok {
			t.Fatal("missing parsed result")
		}
		delete(ti, "parsed")
		if diff := cmp.Diff(input, got); diff != "" {
			t.Fatal(diff)
		}
	}
	for _, ti := range []any{nil, "not an object"} {
		got, err := parser.Preprocess("apply_patch", map[string]any{"tool_input": ti})
		if err != nil {
			t.Fatal(err)
		}
		if got["tool_input"].(map[string]any)["parsed"].(parser.Parsed).Attributes["parse_error"] == nil {
			t.Fatal("missing parse error")
		}
	}
}
