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

// Henia evaluates a skill preload that `henia show` is about to run. The
// preload is presented as a PreToolUse Bash call so existing Bash rules apply.
type Henia struct{}

var _ Adapter = Henia{}

func (Henia) Name() string { return "henia" }

func (Henia) AllowsModify() bool { return true }

func (Henia) ParseInput(raw json.RawMessage) (*envelope.Input, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		var probe any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("henia: malformed input: %w", err)
		}
		return nil, fmt.Errorf("henia: non-object input: got %T", probe)
	}
	var input struct {
		Command string `json:"command"`
		Skill   string `json:"skill"`
		Source  string `json:"source"`
		Tier    string `json:"tier"`
		Caller  string `json:"caller"`
		CWD     string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("henia: malformed input: %w", err)
	}
	switch {
	case strings.TrimSpace(input.Command) == "":
		return nil, errors.New("henia: missing required field command")
	case input.Skill == "":
		return nil, errors.New("henia: missing required field skill")
	case input.Source == "":
		return nil, errors.New("henia: missing required field source")
	case input.Tier != "project" && input.Tier != "global":
		return nil, fmt.Errorf("henia: tier must be project or global, got %q", input.Tier)
	}
	toolInput, err := json.Marshal(map[string]string{"command": input.Command})
	if err != nil {
		return nil, fmt.Errorf("henia: encode command: %w", err)
	}
	return &envelope.Input{
		HookEventName: "PreToolUse",
		ToolName:      "Bash",
		ToolInput:     toolInput,
		CWD:           input.CWD,
		Henia:         &envelope.Henia{Skill: input.Skill, Source: input.Source, Tier: input.Tier, Caller: input.Caller},
	}, nil
}

type heniaResponse struct {
	Decision string `json:"decision"`
	Command  string `json:"command,omitempty"`
	Rule     string `json:"rule,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func (Henia) RenderOutput(out envelope.OutputEnvelope, _ string) (json.RawMessage, error) {
	if out.Category != envelope.Allowing {
		return json.Marshal(heniaResponse{
			Decision: "deny",
			Rule:     out.RuleID,
			Reason:   cmp.Or(strings.TrimSpace(out.UserReason), "Blocked by fas policy"),
		})
	}
	resp := heniaResponse{Decision: "allow"}
	var update struct {
		Command string `json:"command"`
	}
	if len(out.UpdatedInput) > 0 && json.Unmarshal(out.UpdatedInput, &update) == nil && strings.TrimSpace(update.Command) != "" {
		resp.Command = update.Command
	}
	return json.Marshal(resp)
}
