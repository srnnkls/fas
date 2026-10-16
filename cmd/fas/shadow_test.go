package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const sharedInjectRule = `package rules

shared_hint: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
	}
	then: inject: {
		rule_id:  "shared-hint"
		text:     "shared-hint-text"
		channel:  "agent"
		priority: 50
	}
}
`

func TestRun_Eval_ProjectRuleSharingGlobalRuleID_FiresOnce(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{"shared.cue": sharedInjectRule})
	projectDir := writeRuleFiles(t, map[string]string{"shared.cue": sharedInjectRule})

	res := runCLI(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	ctx := decodeCC(t, res.stdout).HookSpecificOutput.AdditionalContext
	if got := strings.Count(ctx, "shared-hint-text"); got != 1 {
		t.Errorf("shared rule_id must inject once, got %d; AdditionalContext=%q", got, ctx)
	}
}

func TestRun_Eval_ProjectCannotReplaceGlobalDenyByReusingRuleID(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{"system.cue": denySystemTargetRule})
	projectDir := writeRuleFiles(t, map[string]string{
		"noop.cue": strings.ReplaceAll(missingKeyRule, `"flags-rule"`, `"r1"`),
	})

	res := runCLI(t, claudeBashInput("rm -rf /etc/passwd"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if got := decodeCC(t, res.stdout).HookSpecificOutput.PermissionDecision; got != "deny" {
		t.Errorf("permissionDecision=%q, want deny from the global rule", got)
	}
}

func TestRun_Vet_ShadowedProjectRule_ReportedAndDeduplicated(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{"shared.cue": sharedInjectRule})
	projectDir := writeRuleFiles(t, map[string]string{
		"shared.cue": sharedInjectRule,
		"system.cue": denySystemTargetRule,
	})

	res := runCLI(t, nil, "vet", "--config", projectDir, "--global-config", globalDir)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	out := string(res.stdout)
	if !strings.Contains(out, "ok: 2 rules loaded (global: 1, project: 1)") {
		t.Errorf("summary must count the de-duplicated set; stdout=%q", out)
	}
	if strings.Contains(out, "project: shared-hint") {
		t.Errorf("shadowed rule must not be listed as a loaded project rule; stdout=%q", out)
	}
	if !strings.Contains(out, "note: project rule shared-hint shadowed by global") {
		t.Errorf("shadowed rule_id must be reported as a note; stdout=%q", out)
	}
}

func TestRun_Vet_ShadowedProjectRule_FormatJSON(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{"shared.cue": sharedInjectRule})
	projectDir := writeRuleFiles(t, map[string]string{
		"shared.cue": sharedInjectRule,
		"system.cue": denySystemTargetRule,
	})

	res := runCLI(t, nil, "vet", "--format", "json", "--config", projectDir, "--global-config", globalDir)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	var summary struct {
		ProjectRules  []string `json:"project_rules"`
		ShadowedRules []string `json:"shadowed_rules"`
		Total         int      `json:"total"`
	}
	if err := json.Unmarshal(res.stdout, &summary); err != nil {
		t.Fatalf("invalid JSON summary: %v\nstdout=%s", err, res.stdout)
	}
	if summary.Total != 2 {
		t.Errorf("total=%d, want 2", summary.Total)
	}
	if len(summary.ProjectRules) != 1 || summary.ProjectRules[0] != "r1" {
		t.Errorf("project_rules=%v, want [r1]", summary.ProjectRules)
	}
	if len(summary.ShadowedRules) != 1 || summary.ShadowedRules[0] != "shared-hint" {
		t.Errorf("shadowed_rules=%v, want [shared-hint]", summary.ShadowedRules)
	}
}
