package pipeline_test

import (
	"os"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/pipeline"
)

// writePackagedRule stages a rule file at rel (a subdir-qualified path) under
// root so the loaded dir contains SUBDIR packages rather than a flat layout.
func writePackagedRule(t *testing.T, root, rel, field, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", p, err)
	}
	src := "package rules\n\n" + field + ": " + body + "\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// TestPackagedLayers_GlobalDenyShortCircuits proves a phase-1 blocking deny
// loaded from a SUBDIR package short-circuits before a matching project rule
// (also from a subdir package) is evaluated.
func TestPackagedLayers_GlobalDenyShortCircuits(t *testing.T) {
	globalDir := t.TempDir()
	writePackagedRule(t, globalDir, filepath.Join("security", "deny.cue"), "g_deny", `{
		when: {tool_name: "Bash"}
		then: deny: {rule_id: "g-deny", reason: "hard no", severity: "HIGH"}
	}`)
	global := loadRules(t, globalDir)

	projectDir := t.TempDir()
	writePackagedRule(t, projectDir, filepath.Join("workflow", "allow.cue"), "p_allow", `{
		when: {tool_name: "Bash"}
		then: allow: true
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 match (phase-1 deny only), got %d: %+v", len(got), got)
	}
	if got[0].Action == nil || got[0].Action.Kind != config.ActionDeny || got[0].Action.RuleID != "g-deny" {
		t.Fatalf("expected phase-1 deny g-deny, got %+v", got[0].Action)
	}
	for _, m := range got {
		if m.Action != nil && m.Action.Kind == config.ActionAllow {
			t.Fatalf("project rule from subdir package must not run on deny short-circuit: %+v", m)
		}
	}
}

// TestPackagedLayers_NonBlockingGlobalRunsProject proves that when the global
// subdir package yields only a non-blocking match, the project subdir package
// is evaluated and concatenated global-then-project in total order.
func TestPackagedLayers_NonBlockingGlobalRunsProject(t *testing.T) {
	globalDir := t.TempDir()
	writePackagedRule(t, globalDir, filepath.Join("security", "ask.cue"), "g_ask", `{
		when: {tool_name: "Bash"}
		then: ask: {rule_id: "g-ask", reason: "ask first", question: "proceed?"}
	}`)
	global := loadRules(t, globalDir)

	projectDir := t.TempDir()
	writePackagedRule(t, projectDir, filepath.Join("workflow", "inject.cue"), "p_inject", `{
		when: {tool_name: "Bash"}
		then: inject: {rule_id: "p-inject", text: "hint", channel: "agent", priority: 50}
	}`)
	project := loadRules(t, projectDir)

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if want := []string{"g-ask", "p-inject"}; !eqSlice(ruleIDs(got), want) {
		t.Fatalf("rule IDs = %v, want %v", ruleIDs(got), want)
	}
}

// TestPackagedLayers_SameRuleNameAcrossLayers_NoCrossContamination proves the
// global and project layers load INDEPENDENTLY: a rule label present in BOTH
// dirs does not trip E0504 (which is within-package only), and each layer keeps
// its own rule.
func TestPackagedLayers_SameRuleNameAcrossLayers_NoCrossContamination(t *testing.T) {
	globalDir := t.TempDir()
	writePackagedRule(t, globalDir, filepath.Join("security", "shared.cue"), "shared", `{
		when: {tool_name: "Bash"}
		then: ask: {rule_id: "global-shared", reason: "g", question: "q?"}
	}`)
	global := loadRules(t, globalDir)

	projectDir := t.TempDir()
	writePackagedRule(t, projectDir, filepath.Join("workflow", "shared.cue"), "shared", `{
		when: {tool_name: "Bash"}
		then: inject: {rule_id: "project-shared", text: "t", channel: "agent", priority: 50}
	}`)
	project := loadRules(t, projectDir)

	if len(global) != 1 || global[0].Then == nil || global[0].Then.RuleID != "global-shared" {
		t.Fatalf("global layer must load its own `shared` rule independently, got %+v", global)
	}
	if len(project) != 1 || project[0].Then == nil || project[0].Then.RuleID != "project-shared" {
		t.Fatalf("project layer must load its own `shared` rule independently, got %+v", project)
	}

	ctx := cuecontext.New()
	input := compileInput(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := pipeline.EvaluatePhases(global, project, input)
	if err != nil {
		t.Fatalf("EvaluatePhases: %v", err)
	}
	if want := []string{"global-shared", "project-shared"}; !eqSlice(ruleIDs(got), want) {
		t.Fatalf("rule IDs = %v, want %v", ruleIDs(got), want)
	}
}
