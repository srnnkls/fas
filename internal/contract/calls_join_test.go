package contract

import (
	"encoding/json"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/parser"
)

func bashInput(t *testing.T, ctx *cue.Context, command string) cue.Value {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input": map[string]any{
			"command": command,
			"parsed":  parser.ParseBash(command),
		},
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	v := ctx.CompileBytes(raw)
	if err := v.Err(); err != nil {
		t.Fatalf("compile input: %v", err)
	}
	return v
}

func matches(pattern, input cue.Value) bool {
	return pattern.Subsume(input, cue.Final(), cue.Schema()) == nil
}

const (
	readVerbsFlat = `commands: list.MatchN(>0, "cat" | "less" | "head")`
	secretFlat    = `targets: list.MatchN(>0, =~"(^|/)\\.env$")`
)

func TestCallsJoin_DistinguishesReadFromCoincidence(t *testing.T) {
	ctx := cuecontext.New()

	flat := ctx.CompileString(`import "list"
{tool_input: parsed: {` + readVerbsFlat + `, ` + secretFlat + `, ...}, ...}`)
	if err := flat.Err(); err != nil {
		t.Fatalf("compile flat pattern: %v", err)
	}

	join := ctx.CompileString(`import "list"
{tool_input: parsed: calls: list.MatchN(>0, {
	command: "cat" | "less" | "head"
	targets: list.MatchN(>0, =~"(^|/)\\.env$")
	...
}), ...}`)
	if err := join.Err(); err != nil {
		t.Fatalf("compile join pattern: %v", err)
	}

	realRead := bashInput(t, ctx, "cat .env")
	coincidence := bashInput(t, ctx, "cat README && rm .env")

	if !matches(flat, realRead) {
		t.Error("flat rule must match a real read of .env")
	}
	if !matches(flat, coincidence) {
		t.Error("flat rule is expected to over-match the coincidence (read verb + secret in separate calls)")
	}

	if !matches(join, realRead) {
		t.Error("join rule must match a real read of .env")
	}
	if matches(join, coincidence) {
		t.Error("join rule must NOT match when no single call both reads and targets the secret")
	}
}
