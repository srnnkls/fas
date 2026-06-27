package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// ccResponse mirrors the Claude Code PreToolUse hook response shape. We decode
// emitted stdout into this struct so key order in the emitted JSON doesn't
// leak into assertions.
type ccResponse struct {
	HookSpecificOutput ccHookSpecificOutput `json:"hookSpecificOutput"`
}

type ccHookSpecificOutput struct {
	HookEventName            string          `json:"hookEventName"`
	PermissionDecision       string          `json:"permissionDecision"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string          `json:"additionalContext,omitempty"`
	AgentMessage             string          `json:"agentMessage,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
}

// writeRuleFiles stages a project (or global) rules directory under
// t.TempDir() by writing each entry of files as <name>.cue. Each body must
// already be a complete CUE source (including any `package` and `import`
// declarations plus the top-level `rule: { ... }` struct).
func writeRuleFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return dir
}

// emptyRulesDir makes an empty directory that can be passed to --config /
// --global-config so the CLI treats the phase as no-rules-loaded.
func emptyRulesDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// runResult bundles the three things every test assertion cares about.
type runResult struct {
	stdout []byte
	stderr []byte
	exit   int
}

// runCLI invokes the in-process entry point with a fresh set of buffers so
// tests never touch the real os.Stdin/Stdout/Stderr.
func runCLI(t *testing.T, stdin []byte, args ...string) runResult {
	t.Helper()
	var stdoutBuf, stderrBuf bytes.Buffer
	code := run(bytes.NewReader(stdin), &stdoutBuf, &stderrBuf, args)
	return runResult{stdout: stdoutBuf.Bytes(), stderr: stderrBuf.Bytes(), exit: code}
}

// decodeCC unmarshals a Claude Code response out of stdout. Fails the test
// if the bytes are not a valid CC response.
func decodeCC(t *testing.T, raw []byte) ccResponse {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("stdout is empty; expected a Claude Code response JSON")
	}
	var resp ccResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("stdout is not valid Claude Code JSON: %v\nraw=%s", err, raw)
	}
	return resp
}

// claudeInput builds a canonical Claude Code PreToolUse payload for the Bash
// tool with the given shell command. transcript_path is included so tests
// exercise the adapter's "tolerate-and-drop" behaviour for unconsumed fields.
func claudeBashInput(command string) []byte {
	type ti struct {
		Command string `json:"command"`
	}
	payload := struct {
		HookEventName  string `json:"hook_event_name"`
		ToolName       string `json:"tool_name"`
		ToolInput      ti     `json:"tool_input"`
		SessionID      string `json:"session_id"`
		CWD            string `json:"cwd"`
		TranscriptPath string `json:"transcript_path"`
	}{
		HookEventName:  "PreToolUse",
		ToolName:       "Bash",
		ToolInput:      ti{Command: command},
		SessionID:      "sess-test",
		CWD:            "/tmp/project",
		TranscriptPath: "/tmp/transcript.jsonl",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic("claudeBashInput: " + err.Error())
	}
	return raw
}

// -----------------------------------------------------------------------------
// Rule fixtures — each is a complete .cue file body. Rule source files cannot
// `import` the fas stdlib through LoadRules (single-file CompileBytes), so
// bodies either stay concrete or inline the stdlib shapes they depend on.
// -----------------------------------------------------------------------------

// denySystemTargetRule blocks Bash calls whose parsed targets look like a
// system path. Mirrors cue/fas.cue's hook.#PreToolUse & tool.#Bash & #hasSystemTarget
// composition inline because LoadRules compiles each file in isolation.
const denySystemTargetRule = `package rules

import (
	"list"
	"strings"
)

_SystemPrefixes: ["/etc", "/sys", "/proc", "/boot", "/dev"]
_systemTarget:   =~"^(\(strings.Join(_SystemPrefixes, "|")))"

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: list.MatchN(>0, _systemTarget)
	}
	then: deny: {
		rule_id:  "r1"
		reason:   "system path"
		severity: "HIGH"
	}
}
`

// askOnBashRule asks for confirmation on any Bash call. Minimal rule for
// exercising the ask path without stdlib scaffolding.
const askOnBashRule = `package rules

ask_rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
	}
	then: ask: {
		rule_id:  "ask-bash"
		reason:   "confirm bash"
		question: "proceed?"
	}
}
`

// destructiveDenyRule blocks any Bash call whose parsed actions include a
// destructive semantic verb — exercises preprocessor output via the pipeline.
const destructiveDenyRule = `package rules

import "list"

_DestructiveActions: ["delete", "drop", "remove", "destroy", "truncate"]
_destructiveAction:  or(_DestructiveActions)

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: actions: list.MatchN(>0, _destructiveAction)
	}
	then: deny: {
		rule_id:  "destructive"
		reason:   "destructive action"
		severity: "HIGH"
	}
}
`

// injectAgentHintRule produces an inject (agent-channel) effect on every Bash
// call. Used to assert effect accumulation across phases.
const injectAgentHintRule = `package rules

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
	}
	then: inject: {
		rule_id:  "hint-a"
		text:     "hint-from-global"
		channel:  "agent"
		priority: 50
	}
}
`

// injectUserHintRule produces an inject (user-channel) effect. Combined with
// injectAgentHintRule to test phase-to-phase accumulation.
const injectUserHintRule = `package rules

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
	}
	then: inject: {
		rule_id:  "hint-b"
		text:     "hint-from-project"
		channel:  "agent"
		priority: 40
	}
}
`

// injectExploreOrientRule injects an agent-channel hint on SubagentStart, but
// only when the starting subagent is the built-in Explore. Concrete fields
// (no stdlib import) so LoadRules compiles the single file in isolation.
const injectExploreOrientRule = `package rules

rule: {
	when: {
		hook_event_name: "SubagentStart"
		agent_type:      "Explore"
	}
	then: inject: {
		rule_id:  "explore-gestalt-orient"
		text:     "ORIENT-WITH-GESTALT"
		channel:  "agent"
		priority: 50
	}
}
`

// injectEmptyGrepHintRule injects on a PostToolUse Grep whose tool_response
// reports zero matches — exercises top-level tool_response plumbing.
const injectEmptyGrepHintRule = `package rules

rule: {
	when: {
		hook_event_name: "PostToolUse"
		tool_name:       "Grep"
		tool_response: numFiles: 0
	}
	then: inject: {
		rule_id:  "empty-grep-hint"
		text:     "GESTALT-MAP-HINT"
		channel:  "agent"
		priority: 50
	}
}
`

// injectSubagentStopRule injects on SubagentStop — exercises the event enum.
const injectSubagentStopRule = `package rules

rule: {
	when: hook_event_name: "SubagentStop"
	then: inject: {
		rule_id:  "subagent-stop-note"
		text:     "SUBAGENT-STOPPED"
		channel:  "agent"
		priority: 50
	}
}
`

// modifyRuleSrc emits a modify effect — must be rejected at rule-load time
// when the Claude Code adapter is selected because CC has no payload-rewrite
// channel.
const modifyRuleSrc = `package rules

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
	}
	then: modify: {
		rule_id:       "rewrite"
		reason:        "rewrite cmd"
		updated_input: {command: "ls"}
		priority:      50
		mode:          "silent"
	}
}
`

// malformedRuleSrc is syntactically invalid CUE — the loader must fail to
// compile it and the error must surface through the CLI.
const malformedRuleSrc = `package rules

rule: {
	when: {hook_event_name: "PreToolUse"
	then: deny: {rule_id: "x", reason: "y"}
}
`

// missingKeyRule references a path segment (`tool_input.flags.force`) that a
// plain Bash payload — `claudeBashInput` emits only `tool_input.command` —
// does not expose. The rule therefore cannot match, and localize must yield
// an E0201 keyed on the absent `flags` label. The `flags` label sits on
// line 8 of the written file (1: `package rules`, 2: blank, 3: `flags_rule: {`,
// 4: `\twhen: {`, 5: `\t\thook_event_name: "PreToolUse"`, 6: `\t\ttool_name:
// "Bash"`, 7: `\t\ttool_input: {`, 8: `\t\t\tflags: force: true`) so stderr
// is expected to carry `flags-miss.cue:8:\d+` when rendered.
const missingKeyRule = `package rules

flags_rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: {
			flags: force: true
		}
	}
	then: deny: {
		rule_id:  "flags-rule"
		reason:   "force flag"
		severity: "HIGH"
	}
}
`

// missingEnvRule is a second miss-variant: it keys on tool_input.env.DEBUG,
// which a plain claudeBashInput payload never carries. Distinct rule_id
// (`env-rule`) and distinct missing label (`env`) so the exact-count test
// can distinguish the two miss diagnostics.
const missingEnvRule = `package rules

env_rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: {
			env: DEBUG: "1"
		}
	}
	then: deny: {
		rule_id:  "env-rule"
		reason:   "debug env set"
		severity: "HIGH"
	}
}
`

// claudeSubagentStartInput builds a canonical Claude Code SubagentStart payload
// for a subagent of the given type. No tool_name / tool_input — SubagentStart
// carries the agent identity instead.
func claudeSubagentStartInput(agentType string) []byte {
	payload := struct {
		HookEventName string `json:"hook_event_name"`
		AgentType     string `json:"agent_type"`
		AgentID       string `json:"agent_id"`
		SessionID     string `json:"session_id"`
		CWD           string `json:"cwd"`
	}{
		HookEventName: "SubagentStart",
		AgentType:     agentType,
		AgentID:       "agent-test",
		SessionID:     "sess-test",
		CWD:           "/tmp/project",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic("claudeSubagentStartInput: " + err.Error())
	}
	return raw
}

// -----------------------------------------------------------------------------
// Allow / deny / ask end-to-end paths
// -----------------------------------------------------------------------------

func TestRun_AllowingPath_EndToEnd(t *testing.T) {
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls -la")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "allow"; got != want {
		t.Errorf("permissionDecision=%q, want %q", got, want)
	}
	if got, want := resp.HookSpecificOutput.HookEventName, "PreToolUse"; got != want {
		t.Errorf("hookEventName=%q, want %q", got, want)
	}
}

func TestRun_BlockingPath_EndToEnd(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"system.cue": denySystemTargetRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("rm -rf /etc/passwd")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "deny"; got != want {
		t.Errorf("permissionDecision=%q, want %q\nstdout=%s", got, want, res.stdout)
	}
	if got, want := resp.HookSpecificOutput.PermissionDecisionReason, "system path"; got != want {
		t.Errorf("permissionDecisionReason=%q, want %q", got, want)
	}
}

func TestRun_AskingPath_EndToEnd(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue": askOnBashRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "ask"; got != want {
		t.Errorf("permissionDecision=%q, want %q\nstdout=%s", got, want, res.stdout)
	}
}

// claudePostToolUseGrepInput builds a PostToolUse payload for Grep carrying a
// top-level tool_response with the given match count.
func claudePostToolUseGrepInput(pattern string, numFiles int) []byte {
	payload := map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Grep",
		"tool_input":      map[string]any{"pattern": pattern},
		"tool_response":   map[string]any{"numFiles": numFiles, "filenames": []string{}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic("claudePostToolUseGrepInput: " + err.Error())
	}
	return raw
}

// claudeSubagentStopInput builds a minimal SubagentStop payload.
func claudeSubagentStopInput() []byte {
	payload := map[string]any{
		"hook_event_name": "SubagentStop",
		"session_id":      "sess-test",
		"cwd":             "/tmp/project",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic("claudeSubagentStopInput: " + err.Error())
	}
	return raw
}

// -----------------------------------------------------------------------------
// PostToolUse tool_response + SubagentStop event
// -----------------------------------------------------------------------------

func TestRun_PostToolUse_ToolResponseRule_Injects(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{
		"grep.cue": injectEmptyGrepHintRule,
	})
	projectDir := emptyRulesDir(t)

	stdin := claudePostToolUseGrepInput("foo", 0)
	res := runCLI(t, stdin,
		"eval", "--harness", "claude",
		"--config", projectDir, "--global-config", globalDir,
	)
	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if !bytes.Contains(res.stdout, []byte("GESTALT-MAP-HINT")) {
		t.Errorf("expected tool_response-keyed inject; stdout=%s", res.stdout)
	}
}

func TestRun_PostToolUse_ToolResponseRule_NoMatchWhenNonZero(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{
		"grep.cue": injectEmptyGrepHintRule,
	})
	projectDir := emptyRulesDir(t)

	stdin := claudePostToolUseGrepInput("foo", 3)
	res := runCLI(t, stdin,
		"eval", "--harness", "claude",
		"--config", projectDir, "--global-config", globalDir,
	)
	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if bytes.Contains(res.stdout, []byte("GESTALT-MAP-HINT")) {
		t.Errorf("non-empty grep must not inject; stdout=%s", res.stdout)
	}
}

func TestRun_SubagentStop_RuleMatches_EndToEnd(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{
		"stop.cue": injectSubagentStopRule,
	})
	projectDir := emptyRulesDir(t)

	stdin := claudeSubagentStopInput()
	res := runCLI(t, stdin,
		"eval", "--harness", "claude",
		"--config", projectDir, "--global-config", globalDir,
	)
	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.HookEventName, "SubagentStop"; got != want {
		t.Errorf("hookEventName=%q, want %q", got, want)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "SUBAGENT-STOPPED") {
		t.Errorf("expected SubagentStop inject; got %q", resp.HookSpecificOutput.AdditionalContext)
	}
	if bytes.Contains(res.stdout, []byte("permissionDecision")) {
		t.Errorf("SubagentStop must not carry permissionDecision; stdout=%s", res.stdout)
	}
}

// -----------------------------------------------------------------------------
// SubagentStart: inject orientation hint into Explore subagents only
// -----------------------------------------------------------------------------

func TestRun_SubagentStart_InjectExploreHint_EndToEnd(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{
		"explore.cue": injectExploreOrientRule,
	})
	projectDir := emptyRulesDir(t)

	stdin := claudeSubagentStartInput("Explore")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.HookEventName, "SubagentStart"; got != want {
		t.Errorf("hookEventName=%q, want %q", got, want)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "ORIENT-WITH-GESTALT") {
		t.Errorf("additionalContext=%q, want it to contain the orient hint", resp.HookSpecificOutput.AdditionalContext)
	}
	if bytes.Contains(res.stdout, []byte("permissionDecision")) {
		t.Errorf("SubagentStart response must not carry permissionDecision; stdout=%s", res.stdout)
	}
}

func TestRun_SubagentStart_UnmatchedAgent_NoInject(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{
		"explore.cue": injectExploreOrientRule,
	})
	projectDir := emptyRulesDir(t)

	stdin := claudeSubagentStartInput("general-purpose")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if bytes.Contains(res.stdout, []byte("ORIENT-WITH-GESTALT")) {
		t.Errorf("an agent_type the rule does not target must not receive the hint; stdout=%s", res.stdout)
	}
}

// -----------------------------------------------------------------------------
// Two-phase evaluation: global short-circuits project
// -----------------------------------------------------------------------------

func TestRun_GlobalRuleBlocks_ShortCircuit(t *testing.T) {
	// Global denies; project would inject. Blocking gate must win AND the
	// project rule's inject must not leak into additionalContext (effects
	// accumulate, but we expect only the global deny ran — the project
	// phase never runs on blocking short-circuit).
	globalDir := writeRuleFiles(t, map[string]string{
		"system.cue": denySystemTargetRule,
	})
	projectDir := writeRuleFiles(t, map[string]string{
		"inject.cue": injectUserHintRule,
	})

	stdin := claudeBashInput("rm -rf /etc/passwd")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "deny"; got != want {
		t.Fatalf("permissionDecision=%q, want %q; stdout=%s", got, want, res.stdout)
	}
	// The project phase must not have run — its inject text ("hint-from-project")
	// must be absent from the response.
	if strings.Contains(string(res.stdout), "hint-from-project") {
		t.Errorf("project inject leaked into response; phase 2 should have been skipped.\nstdout=%s", res.stdout)
	}
}

func TestRun_EffectsAccumulate_AcrossPhases(t *testing.T) {
	// Both phases non-blocking; both emit inject effects. The synthesizer
	// should combine them into AdditionalContext (agent channel).
	globalDir := writeRuleFiles(t, map[string]string{
		"inject_global.cue": injectAgentHintRule,
	})
	projectDir := writeRuleFiles(t, map[string]string{
		"inject_project.cue": injectUserHintRule,
	})

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "allow"; got != want {
		t.Fatalf("permissionDecision=%q, want %q", got, want)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "hint-from-global") {
		t.Errorf("global inject text missing from AdditionalContext; got %q", ctx)
	}
	if !strings.Contains(ctx, "hint-from-project") {
		t.Errorf("project inject text missing from AdditionalContext; got %q", ctx)
	}
}

// -----------------------------------------------------------------------------
// Adapter capability check: modify rejected at load for Claude
// -----------------------------------------------------------------------------

func TestRun_ModifyRule_RejectedAtLoad_ForClaudeAdapter(t *testing.T) {
	// A modify rule with the claude harness must fail to load: CC's PreToolUse
	// hook has no payload-rewrite channel, and rules emitting unsupported
	// effects are rejected at rule-load time per the Adapter Capabilities
	// section of design.md.
	projectDir := writeRuleFiles(t, map[string]string{
		"modify.cue": modifyRuleSrc,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit == 0 {
		t.Fatalf("expected non-zero exit for modify+claude; got 0\nstdout=%s\nstderr=%s",
			res.stdout, res.stderr)
	}
	errMsg := string(res.stderr)
	if !strings.Contains(errMsg, "modify.cue") {
		t.Errorf("stderr must name the offending rule file; got %q", errMsg)
	}
	if !strings.Contains(strings.ToLower(errMsg), "modify") {
		t.Errorf("stderr must mention the unsupported effect (modify); got %q", errMsg)
	}
	if !strings.Contains(strings.ToLower(errMsg), "claude") {
		t.Errorf("stderr must name the harness (claude); got %q", errMsg)
	}
}

// -----------------------------------------------------------------------------
// Fail-open vs fail-closed on engine errors
// -----------------------------------------------------------------------------

func TestRun_MissingHookEventName_FailOpen_DefaultsAllow(t *testing.T) {
	// Default behaviour: fail-open. A malformed input (no hook_event_name)
	// should still produce an Allowing envelope and a zero exit so the
	// hook never breaks a user's workflow on engine bugs.
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	stdin := []byte(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0 (fail-open should never exit non-zero on engine error); stderr=%s",
			res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "allow"; got != want {
		t.Errorf("permissionDecision=%q, want %q (fail-open default)", got, want)
	}
}

func TestRun_MissingHookEventName_FailClosed_EmitsBlocking(t *testing.T) {
	// --fail-closed flips the engine to emit Blocking on parse/engine errors.
	// The exit code stays 0 because the evaluation *structurally* succeeded;
	// it just blocked.
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	stdin := []byte(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--fail-closed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0 (fail-closed still returns 0 — the eval produced a decision); stderr=%s",
			res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "deny"; got != want {
		t.Fatalf("permissionDecision=%q, want %q (fail-closed must block); stdout=%s",
			got, want, res.stdout)
	}
	reason := resp.HookSpecificOutput.PermissionDecisionReason
	if reason == "" {
		t.Errorf("fail-closed deny must include a reason naming the parse failure; got empty")
	}
}

// -----------------------------------------------------------------------------
// Config path handling
// -----------------------------------------------------------------------------

func TestRun_ConfigPathDoesNotExist_TreatedAsEmpty(t *testing.T) {
	// A nonexistent rules directory is equivalent to an empty ruleset. The
	// pipeline should default to allowing.
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist")
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", nonexistent,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "allow"; got != want {
		t.Errorf("permissionDecision=%q, want %q (missing rules dir should be empty, not fatal)",
			got, want)
	}
}

func TestRun_MalformedRuleFile_NonZeroExit(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"bad.cue": malformedRuleSrc,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit == 0 {
		t.Fatalf("expected non-zero exit for malformed rule; got 0\nstdout=%s\nstderr=%s",
			res.stdout, res.stderr)
	}
	if !strings.Contains(string(res.stderr), "bad.cue") {
		t.Errorf("stderr must name the offending file; got %q", res.stderr)
	}
}

// -----------------------------------------------------------------------------
// CLI surface: harness validation, help
// -----------------------------------------------------------------------------

func TestRun_UnknownHarness_NonZeroExit(t *testing.T) {
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "bogus",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit == 0 {
		t.Fatalf("expected non-zero exit for unknown harness; got 0")
	}
	// stderr should list the valid harnesses (at minimum: claude).
	if !strings.Contains(strings.ToLower(string(res.stderr)), "claude") {
		t.Errorf("stderr should list valid harnesses (claude); got %q", res.stderr)
	}
}

func TestRun_Help_ZeroExit(t *testing.T) {
	cases := []string{"--help", "-h"}
	for _, flag := range cases {
		t.Run(flag, func(t *testing.T) {
			res := runCLI(t, nil, flag)
			if res.exit != 0 {
				t.Fatalf("%s: exit=%d want 0", flag, res.exit)
			}
			// Usage text must appear on stdout or stderr; accept either since
			// flag.PrintDefaults writes to a writer the CLI controls.
			combined := string(res.stdout) + string(res.stderr)
			if !strings.Contains(strings.ToLower(combined), "usage") &&
				!strings.Contains(strings.ToLower(combined), "eval") {
				t.Errorf("%s: expected usage text on stdout/stderr; got stdout=%q stderr=%q",
					flag, res.stdout, res.stderr)
			}
		})
	}
}

// TestRun_Version_PrintsAndExitsZero asserts --version short-circuits before
// rule loading or stdin reads, prints `fas <version>` on stdout, and exits 0.
// The default version sentinel "dev" appears under `go test`; release builds
// override it via -X main.version=<tag>.
func TestRun_Version_PrintsAndExitsZero(t *testing.T) {
	res := runCLI(t, nil, "--version")
	if res.exit != 0 {
		t.Fatalf("--version: exit=%d want 0; stderr=%q", res.exit, res.stderr)
	}
	out := string(res.stdout)
	if !strings.HasPrefix(out, "fas ") {
		t.Errorf("--version stdout must start with %q; got %q", "fas ", out)
	}
	if strings.TrimSpace(out) == "fas" {
		t.Errorf("--version stdout must carry a version token after %q; got %q", "fas", out)
	}
}

// -----------------------------------------------------------------------------
// Preprocessor integration: parser verbs feed stdlib-style rules
// -----------------------------------------------------------------------------

func TestRun_Preprocess_IsApplied_BashVerbsInActions(t *testing.T) {
	// Rule keys on parsed.actions containing a destructive verb. If the
	// preprocessor doesn't run, actions is empty and the rule never fires.
	// Matching proves parse ran AND the verb was extracted.
	projectDir := writeRuleFiles(t, map[string]string{
		"destructive.cue": destructiveDenyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("rm -rf /etc/passwd")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "deny"; got != want {
		t.Fatalf("permissionDecision=%q, want %q (preprocess must extract 'remove' from `rm`)",
			got, want)
	}
	if got, want := resp.HookSpecificOutput.PermissionDecisionReason, "destructive action"; got != want {
		t.Errorf("permissionDecisionReason=%q, want %q", got, want)
	}
}

func TestRun_BashInputWithForLoop_StillCatchesRm(t *testing.T) {
	// Regression guard for the T4 AST-walking fix: `for f in *; do rm $f; done`
	// must still surface "remove" in parsed.actions so a #hasDestructiveAction
	// rule matches. A naive first-command extractor would miss this.
	projectDir := writeRuleFiles(t, map[string]string{
		"destructive.cue": destructiveDenyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("for f in *; do rm $f; done")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "deny"; got != want {
		t.Fatalf("permissionDecision=%q, want %q (for-loop should still expose nested `rm`)",
			got, want)
	}
}

// -----------------------------------------------------------------------------
// Determinism
// -----------------------------------------------------------------------------

func TestRun_DeterministicOutput(t *testing.T) {
	// Same input + same rules => byte-identical stdout across runs.
	globalDir := writeRuleFiles(t, map[string]string{
		"inject_global.cue": injectAgentHintRule,
	})
	projectDir := writeRuleFiles(t, map[string]string{
		"inject_project.cue": injectUserHintRule,
	})

	stdin := claudeBashInput("ls")

	first := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if first.exit != 0 {
		t.Fatalf("first run: exit=%d stderr=%s", first.exit, first.stderr)
	}

	second := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if second.exit != 0 {
		t.Fatalf("second run: exit=%d stderr=%s", second.exit, second.stderr)
	}

	if !bytes.Equal(first.stdout, second.stdout) {
		t.Errorf("non-deterministic stdout:\n first=%s\n second=%s",
			first.stdout, second.stdout)
	}
}

// -----------------------------------------------------------------------------
// --explain flag (T7) — diagnostics to stderr, stdout unchanged
// -----------------------------------------------------------------------------
//
// The --explain flag flips the evaluator's package-level explain toggle on,
// causing localize to emit diagnostics for non-matching rules. Filter values:
//
//   --explain           → defaults to `missed` (one diagnostic per non-match)
//   --explain=missed    → only non-match diagnostics
//   --explain=fired     → only firing-rule traces (miss diagnostics suppressed)
//   --explain=both      → both firing traces and miss diagnostics
//
// Diagnostics are written to stderr AFTER the normal vendor response on
// stdout. Without the flag, stderr stays empty and the evaluator's fast path
// (no localize calls) is preserved — no cost is paid for diagnostics users
// never requested.

// explainStderrHasPositionAnchor returns true if stderr contains a CUE-file
// position substring of the form `<name>.cue:<line>:<col>`. This is the
// "position resolution worked" anchor: it fails if the renderer degraded to
// `position unknown`.
func explainStderrHasPositionAnchor(stderr []byte) bool {
	// Scan for `.cue:<digits>:<digits>` without pulling in the regexp package
	// — the existing file stays in the stdlib set it already imports.
	s := string(stderr)
	for idx := strings.Index(s, ".cue:"); idx >= 0; idx = strings.Index(s, ".cue:") {
		rest := s[idx+len(".cue:"):]
		// Require at least one digit, a colon, and another digit.
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i > 0 && i < len(rest) && rest[i] == ':' {
			j := i + 1
			for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
				j++
			}
			if j > i+1 {
				return true
			}
		}
		s = rest
	}
	return false
}

// TestRun_Explain_MissedEmitsDiagnosticsOnStderr covers the core acceptance
// criterion: --explain with a non-firing rule emits one diagnostic on stderr,
// AND the normal vendor response still lands on stdout unchanged.
func TestRun_Explain_MissedEmitsDiagnosticsOnStderr(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=missed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0 (explain must not fail the pipeline); stderr=%s",
			res.exit, res.stderr)
	}
	// stdout must still carry the vendor response — --explain never replaces
	// the normal output, it augments it on stderr.
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "allow"; got != want {
		t.Errorf("permissionDecision=%q, want %q (rule didn't match so allow)", got, want)
	}
	if len(res.stderr) == 0 {
		t.Fatalf("--explain with one non-firing rule: stderr should carry a diagnostic, got empty")
	}
	if !strings.Contains(string(res.stderr), "E0201") {
		t.Errorf("stderr should contain E0201 (absent-key diagnostic); got %q", res.stderr)
	}
	// Forward-protect against unbounded diagnostic accumulation: a single
	// non-firing rule must produce exactly one E0201 diagnostic. If a future
	// regression causes the evaluator to re-emit the same diagnostic per
	// walker pass, this count trips before the count propagates out.
	if got := strings.Count(string(res.stderr), "E0201"); got != 1 {
		t.Errorf("expected exactly 1 E0201 diagnostic for one non-firing rule; got %d\nstderr=%s",
			got, res.stderr)
	}
}

// TestRun_Explain_MissedDefault covers the bare --explain form, which must
// default to the `missed` filter. The strong form of this assertion is that
// bare --explain produces byte-identical stderr to --explain=missed — a bug
// where the default silently widened to `both` or `fired` would produce
// diverging stderr even if both runs contained an E0201 substring.
func TestRun_Explain_MissedDefault(t *testing.T) {
	// Two distinct projectDirs (clean per subtest) but identical rule file
	// contents and identical stdin, so stderr must be byte-equal.
	stdin := claudeBashInput("ls")

	bareProjectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	bareGlobalDir := emptyRulesDir(t)
	bare := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", bareProjectDir,
		"--global-config", bareGlobalDir,
		"--explain",
	)
	if bare.exit != 0 {
		t.Fatalf("bare --explain: exit=%d want 0; stderr=%s", bare.exit, bare.stderr)
	}

	explicitProjectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	explicitGlobalDir := emptyRulesDir(t)
	explicit := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", explicitProjectDir,
		"--global-config", explicitGlobalDir,
		"--explain=missed",
	)
	if explicit.exit != 0 {
		t.Fatalf("--explain=missed: exit=%d want 0; stderr=%s", explicit.exit, explicit.stderr)
	}

	// Normalize the per-run tempdir path out of stderr before comparing. The
	// two runs use different tempdirs so a raw bytes.Equal over stderr would
	// trivially disagree on the path prefix; we substitute a stable token for
	// each run's projectDir.
	bareNorm := bytes.ReplaceAll(bare.stderr, []byte(bareProjectDir), []byte("<PROJECT>"))
	explicitNorm := bytes.ReplaceAll(explicit.stderr, []byte(explicitProjectDir), []byte("<PROJECT>"))
	if !bytes.Equal(bareNorm, explicitNorm) {
		t.Errorf("bare --explain must be equivalent to --explain=missed;\n bare stderr=%s\n explicit stderr=%s",
			bareNorm, explicitNorm)
	}

	// Baseline sanity: both runs should have emitted the E0201 miss diagnostic.
	if !strings.Contains(string(bare.stderr), "E0201") {
		t.Errorf("--explain (bare) must default to missed and show E0201 for a non-match; stderr=%q",
			bare.stderr)
	}
}

// TestRun_Explain_FiredOnlyFiringRules checks the `fired` filter: with one
// rule that fires and one that misses, stderr must reference the fired rule
// and must NOT carry the missed rule's E-code.
func TestRun_Explain_FiredOnlyFiringRules(t *testing.T) {
	// Two rules: askOnBashRule always matches a Bash payload; missingKeyRule
	// never matches (flags.force absent). With --explain=fired, only the
	// matching rule should surface in the trace.
	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue":        askOnBashRule,
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=fired",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	stderrStr := string(res.stderr)
	if len(stderrStr) == 0 {
		t.Fatalf("--explain=fired with one matching rule: stderr should carry a fired trace, got empty")
	}
	// The fired rule's rule_id is "ask-bash" (from askOnBashRule's
	// then.ask.rule_id). The trace should mention it.
	if !strings.Contains(stderrStr, "ask-bash") {
		t.Errorf("--explain=fired must mention the fired rule_id `ask-bash`; stderr=%q", stderrStr)
	}
	// And the missed rule's E-code must NOT appear under the `fired` filter.
	if strings.Contains(stderrStr, "E0201") {
		t.Errorf("--explain=fired must suppress missed-rule diagnostics; got E0201 in stderr=%q",
			stderrStr)
	}
}

// TestRun_Explain_BothShowsFiredAndMissed covers the `both` filter: both the
// firing rule's trace and the missed rule's E-code must appear on stderr.
func TestRun_Explain_BothShowsFiredAndMissed(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue":        askOnBashRule,
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=both",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	stderrStr := string(res.stderr)
	if !strings.Contains(stderrStr, "ask-bash") {
		t.Errorf("--explain=both must mention the fired rule_id `ask-bash`; stderr=%q", stderrStr)
	}
	if !strings.Contains(stderrStr, "E0201") {
		t.Errorf("--explain=both must include the missed rule's E0201; stderr=%q", stderrStr)
	}
}

// TestRun_Explain_FiredWithNoFiringRules asserts the `fired` filter is honest:
// when no rules fire (only a missing-key rule is loaded), stderr must be
// empty — the filter must not leak miss diagnostics through.
func TestRun_Explain_FiredWithNoFiringRules(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=fired",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if len(res.stderr) != 0 {
		t.Errorf("--explain=fired with zero firing rules must emit empty stderr; got %q",
			res.stderr)
	}
}

// TestRun_Explain_WithoutFlag_StderrEmpty confirms the zero-cost path: when
// --explain is absent, the evaluator's toggle stays off and stderr remains
// empty even with a non-firing rule that would otherwise trigger localize.
func TestRun_Explain_WithoutFlag_StderrEmpty(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if len(res.stderr) != 0 {
		t.Errorf("without --explain, stderr must stay empty (fast path); got %q", res.stderr)
	}
	// Stdout path is still the normal allowing response.
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "allow"; got != want {
		t.Errorf("permissionDecision=%q, want %q", got, want)
	}
}

// TestRun_Explain_InvalidValue_NonZeroExit asserts input validation: bogus
// filter names are rejected, and the error message lists the allowed values
// so the user can self-correct. Exit code must be non-zero; we don't pin the
// exact code (flag.Parse conventionally returns 2, but a custom validator
// could return 1 — the contract is "refuse to run", not the specific code).
func TestRun_Explain_InvalidValue_NonZeroExit(t *testing.T) {
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=bogus",
	)

	if res.exit == 0 {
		t.Fatalf("exit=%d want non-zero (flag validation failure); stderr=%s",
			res.exit, res.stderr)
	}
	stderrStr := strings.ToLower(string(res.stderr))
	if !strings.Contains(stderrStr, "bogus") {
		t.Errorf("stderr should name the bad value `bogus`; got %q", res.stderr)
	}
	// User must be told what's acceptable. Each valid value should appear
	// in the diagnostic so the user can pick one without consulting docs.
	for _, valid := range []string{"fired", "missed", "both"} {
		if !strings.Contains(stderrStr, valid) {
			t.Errorf("stderr should list valid value %q among accepted values; got %q",
				valid, res.stderr)
		}
	}
}

// parseExplainLineCol extracts the line and column from the first
// `.cue:<line>:<col>` substring in stderr. Returns ok=false if none is
// found. Avoids importing regexp so this test file keeps its minimal
// import set.
func parseExplainLineCol(stderr []byte) (line, col int, ok bool) {
	s := string(stderr)
	_, after, ok0 := strings.Cut(s, ".cue:")
	if !ok0 {
		return 0, 0, false
	}
	rest := after
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		line = line*10 + int(rest[i]-'0')
		i++
	}
	if i == 0 || i >= len(rest) || rest[i] != ':' {
		return 0, 0, false
	}
	j := i + 1
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		col = col*10 + int(rest[j]-'0')
		j++
	}
	if j == i+1 {
		return 0, 0, false
	}
	return line, col, true
}

// TestRun_Explain_DiagnosticCarriesPositionAnchor is the position-resolution
// anchor (scope review memory H1): stderr must contain a real `file:line:col`
// substring, not the degraded `position unknown` marker. Without this, a
// renderer that silently dropped positions would pass all the E-code / rule_id
// assertions above.
//
// We additionally pin the line number into the rule file's actual line range
// — a renderer emitting `flags-miss.cue:999:999` must not pass. Column is
// kept loose (CUE's token positions can point at operators or spans).
func TestRun_Explain_DiagnosticCarriesPositionAnchor(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=missed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if !strings.Contains(string(res.stderr), "flags-miss.cue") {
		t.Errorf("stderr must reference the rule file `flags-miss.cue`; got %q", res.stderr)
	}
	if !explainStderrHasPositionAnchor(res.stderr) {
		t.Errorf("stderr must carry a `<file>.cue:<line>:<col>` substring proving position resolution; got %q",
			res.stderr)
	}
	if strings.Contains(string(res.stderr), "position unknown") {
		t.Errorf("stderr must not carry `position unknown` marker; got %q", res.stderr)
	}

	// Pin the line anchor into the file's actual line range. Read the rule
	// file from projectDir and count its newlines; the diagnostic's line
	// must fall within 1..fileLineCount.
	rulePath := filepath.Join(projectDir, "flags-miss.cue")
	ruleBytes, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read back rule file: %v", err)
	}
	fileLineCount := bytes.Count(ruleBytes, []byte("\n"))
	// bytes.Count on "\n" yields newline count; if the file doesn't end with
	// a newline the last line wouldn't be counted — add one to cover the
	// trailing line regardless.
	if !bytes.HasSuffix(ruleBytes, []byte("\n")) {
		fileLineCount++
	}
	line, _, ok := parseExplainLineCol(res.stderr)
	if !ok {
		t.Fatalf("could not parse <file>.cue:<line>:<col> out of stderr=%q", res.stderr)
	}
	if line < 1 || line > fileLineCount {
		t.Errorf("position anchor line=%d out of file range [1..%d]; stderr=%q",
			line, fileLineCount, res.stderr)
	}
}

// TestRun_Explain_StdoutCarriesVendorResponse is a paranoia anchor for the
// ordering contract: diagnostics go to stderr, the vendor response goes to
// stdout, and the response must remain valid JSON. A naive implementation
// that routed diagnostics to stdout would corrupt the response parser.
//
// Temporal ordering (stdout flush before stderr write) is a visible-terminal
// concern not observable from separate bytes.Buffers; this test only pins
// the CHANNEL SEPARATION contract (stdout = vendor JSON, stderr =
// diagnostics), not the interleaving of bytes on a terminal. Interleaving
// is an operational property, not a unit-test property.
func TestRun_Explain_StdoutCarriesVendorResponse(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=missed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	// stdout must be a standalone valid CC response (no diagnostic spillover).
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.HookEventName, "PreToolUse"; got != want {
		t.Errorf("hookEventName=%q, want %q; stdout polluted by diagnostics?", got, want)
	}
	// stderr must NOT contain the vendor JSON envelope keys — that would
	// mean diagnostics leaked onto stdout or vice versa.
	if strings.Contains(string(res.stderr), "hookSpecificOutput") {
		t.Errorf("stderr must not carry vendor JSON; got %q", res.stderr)
	}
}

// TestRun_Explain_ToggleResetsBetweenRuns pins that the CLI resets the
// package-level explainToggle between invocations.
//
// Guards against a leak of evaluator's package-level explainToggle across
// in-process run() invocations. runCLI shares the fas binary's process,
// so if run() calls SetExplainEnabled(true) on --explain but never calls
// SetExplainEnabled(false) when the flag is absent, a subsequent
// no-flag invocation in the same test process would still have explain
// enabled. This test exercises that exact sequence (on → off) and
// asserts the second run's stderr is empty.
//
// Note: when this test runs standalone from a fresh process, the
// TestRun_Explain_WithoutFlag_StderrEmpty guard covers the no-flag path
// directly. The additional value of this test is the SEQUENCE:
// --explain=missed (toggle ON) followed by bare run (toggle must be reset
// to OFF). A test runner parallelism bug that observed first.stderr empty
// would mean the test couldn't detect a leak because the first call never
// emitted anything — we log a t.Logf in that defensive case so the reader
// knows why a green status wouldn't prove absence of the leak.
func TestRun_Explain_ToggleResetsBetweenRuns(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")

	// First run: --explain on → stderr should carry a diagnostic.
	first := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=missed",
	)
	if first.exit != 0 {
		t.Fatalf("first run exit=%d want 0; stderr=%s", first.exit, first.stderr)
	}
	if len(first.stderr) == 0 {
		// Defensive: if the first run didn't emit, this test can't prove the
		// leak-reset invariant. Surface that clearly instead of silently
		// green-lighting the run.
		t.Logf("first run (--explain=missed) stderr is empty; leak-detection vacuous for this run")
		t.Fatalf("first run (--explain=missed) should emit diagnostics; got empty stderr")
	}

	// Second run: --explain absent → stderr must be empty. If the CLI never
	// calls SetExplainEnabled(false), the package-level toggle would still
	// be `true` from the first run and localize would keep firing.
	second := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if second.exit != 0 {
		t.Fatalf("second run exit=%d want 0; stderr=%s", second.exit, second.stderr)
	}
	if len(second.stderr) != 0 {
		t.Errorf("second run (no --explain) must have empty stderr; leak from first run detected. stderr=%q",
			second.stderr)
	}

	// Third run: no --explain again. If the leak existed, it would still be
	// observable here (toggle wasn't reset). This extra hop makes the
	// assertion robust even if a future implementation only flipped the
	// toggle within the first couple of calls.
	third := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if third.exit != 0 {
		t.Fatalf("third run exit=%d want 0; stderr=%s", third.exit, third.stderr)
	}
	if len(third.stderr) != 0 {
		t.Errorf("third run (no --explain) must have empty stderr; toggle leaked. stderr=%q",
			third.stderr)
	}
}

// TestRun_Explain_ThreeRules_ExactlyTwoDiagnostics is the canonical acceptance
// test for F7 (scope acceptance line 81): "three rules (one fires, two
// don't), stderr contains exactly two diagnostics". Pins the COUNT — substring
// assertions alone can't tell the difference between 2, 5, or 20 diagnostics.
func TestRun_Explain_ThreeRules_ExactlyTwoDiagnostics(t *testing.T) {
	// Three rules in the project dir:
	//   - ask.cue         → askOnBashRule fires on any Bash payload
	//   - flags-miss.cue  → missingKeyRule  misses (flags.force absent)
	//   - env-miss.cue    → missingEnvRule  misses (env.DEBUG absent)
	// The two miss-rules have distinct rule_ids (flags-rule, env-rule) so
	// they produce distinguishable diagnostics. The fired rule's rule_id is
	// "ask-bash" — it must NOT appear in stderr (miss-only filter).
	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue":        askOnBashRule,
		"flags-miss.cue": missingKeyRule,
		"env-miss.cue":   missingEnvRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=missed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}

	// stdout still carries the vendor response; the fired ask rule wins the
	// decision.
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "ask"; got != want {
		t.Errorf("permissionDecision=%q, want %q", got, want)
	}

	stderrStr := string(res.stderr)

	// Exactly two diagnostics on stderr. Both misses produce E0201 (absent
	// key); the count tolerates either "error[E02" or the bare code but we
	// pick "E0201" which is the specific absent-key code.
	if got := strings.Count(stderrStr, "E0201"); got != 2 {
		t.Errorf("want exactly 2 E0201 diagnostics (one per missed rule); got %d\nstderr=%s",
			got, stderrStr)
	}

	// Both missed rule_ids must appear so the user can tie each diagnostic
	// back to its rule.
	if !strings.Contains(stderrStr, "flags-rule") {
		t.Errorf("stderr missing flags-rule rule_id; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "env-rule") {
		t.Errorf("stderr missing env-rule rule_id; got %q", stderrStr)
	}

	// The firing rule's rule_id must NOT appear under --explain=missed.
	if strings.Contains(stderrStr, "ask-bash") {
		t.Errorf("--explain=missed must not surface fired rule `ask-bash`; stderr=%q", stderrStr)
	}
}

// TestRun_Explain_Fired_DoesNotInterfereWithBlockingDecision asserts that
// --explain=fired is side-effect free with respect to the vendor decision:
// a blocking rule still produces exit 0 with a deny decision on stdout, and
// the fired rule's rule_id appears in the stderr trace.
func TestRun_Explain_Fired_DoesNotInterfereWithBlockingDecision(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"system.cue": denySystemTargetRule,
	})
	globalDir := emptyRulesDir(t)

	// The denySystemTargetRule fires on a system-path target.
	stdin := claudeBashInput("rm -rf /etc/passwd")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=fired",
	)

	// Evaluation succeeded (decision made) — exit is 0 even with a block.
	if res.exit != 0 {
		t.Fatalf("exit=%d want 0 (deny is a valid decision, not an engine error); stderr=%s",
			res.exit, res.stderr)
	}

	// stdout carries the vendor deny response. The flag must not redirect
	// the decision to stderr or drop it.
	if !strings.Contains(string(res.stdout), `"permissionDecision":"deny"`) {
		t.Errorf("stdout must contain the deny decision; got %q", res.stdout)
	}

	// stderr carries the fired rule's trace; denySystemTargetRule's rule_id
	// is "r1".
	if !strings.Contains(string(res.stderr), "r1") {
		t.Errorf("--explain=fired must reference fired rule_id `r1`; stderr=%q", res.stderr)
	}
}

// -----------------------------------------------------------------------------
// FAS_EXPLAIN env var (T8) — env fallback for the --explain flag
// -----------------------------------------------------------------------------
//
// FAS_EXPLAIN accepts truthy values (1, true, yes; case-insensitive) and, when
// set, enables the same behavior as --explain=missed. The explicit --explain
// flag always wins when both are set (even if the flag's value is a stricter
// or broader filter than the env-var default of `missed`). Non-truthy values
// (0, false, no, empty, arbitrary strings) leave the env fallback off, so the
// zero-cost fast path is preserved for users who haven't opted in.
//
// All tests use t.Setenv so env state is isolated per-test and automatically
// restored on teardown — no leakage across the rest of TestRun_* can occur.

// TestRun_FasExplain_TruthyEnablesMissed covers the baseline contract:
// FAS_EXPLAIN=1 with no --explain flag should behave exactly like passing
// --explain=missed. A non-firing rule must surface its E0201 diagnostic on
// stderr, and the vendor response on stdout must remain intact.
func TestRun_FasExplain_TruthyEnablesMissed(t *testing.T) {
	t.Setenv("FAS_EXPLAIN", "1")

	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0 (env-driven explain must not fail pipeline); stderr=%s",
			res.exit, res.stderr)
	}
	// Vendor response must still land on stdout.
	resp := decodeCC(t, res.stdout)
	if got, want := resp.HookSpecificOutput.PermissionDecision, "allow"; got != want {
		t.Errorf("permissionDecision=%q, want %q", got, want)
	}
	if len(res.stderr) == 0 {
		t.Fatalf("FAS_EXPLAIN=1 must enable missed diagnostics; stderr is empty")
	}
	if !strings.Contains(string(res.stderr), "E0201") {
		t.Errorf("stderr should carry E0201 when FAS_EXPLAIN=1; got %q", res.stderr)
	}
}

// TestRun_FasExplain_FlagWinsOverEnv asserts that --explain=both combined
// with FAS_EXPLAIN=1 produces `both` output (fired rule_id AND missed
// E-code), NOT just `missed`. The flag is authoritative.
func TestRun_FasExplain_FlagWinsOverEnv(t *testing.T) {
	t.Setenv("FAS_EXPLAIN", "1")

	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue":        askOnBashRule,
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=both",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	stderrStr := string(res.stderr)
	// Fired trace (from --explain=both) — env-driven `missed` would omit this.
	if !strings.Contains(stderrStr, "ask-bash") {
		t.Errorf("--explain=both must show fired rule_id `ask-bash` (flag wins over env); stderr=%q",
			stderrStr)
	}
	// Missed E-code — present in both `missed` and `both`, so this alone
	// can't distinguish; the fired-trace check above is the discriminator.
	if !strings.Contains(stderrStr, "E0201") {
		t.Errorf("--explain=both must show missed E0201; stderr=%q", stderrStr)
	}
}

// TestRun_FasExplain_FlagWinsOverEnv_MoreRestrictive is the negative form of
// the precedence check: FAS_EXPLAIN=1 (which would enable `missed`) combined
// with --explain=fired must produce fired-only output. If the implementation
// OR'd env+flag filters instead of letting the flag win, we'd see E0201 in
// stderr. Pins that the flag wins even when its filter is STRICTER than env.
func TestRun_FasExplain_FlagWinsOverEnv_MoreRestrictive(t *testing.T) {
	t.Setenv("FAS_EXPLAIN", "1")

	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue":        askOnBashRule,
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=fired",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	stderrStr := string(res.stderr)
	// Fired trace must appear.
	if !strings.Contains(stderrStr, "ask-bash") {
		t.Errorf("--explain=fired must include fired rule_id `ask-bash`; stderr=%q", stderrStr)
	}
	// Missed E-code must NOT leak through. If the env widened the filter,
	// this would trip.
	if strings.Contains(stderrStr, "E0201") {
		t.Errorf("--explain=fired with FAS_EXPLAIN=1 must suppress missed diagnostics (flag wins); got E0201 in stderr=%q",
			stderrStr)
	}
}

// TestRun_FasExplain_FalsyDisabled covers FAS_EXPLAIN=0. The env var is
// present but non-truthy, so stderr must remain empty for a non-firing rule.
// This distinguishes "env falsy" from "env unset" (separate test below) so a
// bug that treated any non-empty value as truthy would trip here.
func TestRun_FasExplain_FalsyDisabled(t *testing.T) {
	t.Setenv("FAS_EXPLAIN", "0")

	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if len(res.stderr) != 0 {
		t.Errorf("FAS_EXPLAIN=0 must keep stderr empty (explain disabled); got %q",
			res.stderr)
	}
}

// TestRun_FasExplain_UnsetDisabled confirms the zero-cost default: when the
// env var is not present in the environment at all, stderr stays empty. This
// tests the UNSET case specifically (os.LookupEnv returns ok=false), as
// distinct from the empty-string case covered in NonTruthyVariants.
// Implementations that use os.LookupEnv to distinguish unset-vs-empty must
// still treat unset as disabled.
func TestRun_FasExplain_UnsetDisabled(t *testing.T) {
	// Take ownership of FAS_EXPLAIN: record prior, remove it, restore on
	// cleanup. t.Setenv("") is NOT unset (LookupEnv still returns ok=true), so
	// we use os.Unsetenv directly and manage restoration ourselves.
	prior, had := os.LookupEnv("FAS_EXPLAIN")
	if err := os.Unsetenv("FAS_EXPLAIN"); err != nil {
		t.Fatalf("os.Unsetenv: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("FAS_EXPLAIN", prior)
		} else {
			_ = os.Unsetenv("FAS_EXPLAIN")
		}
	})

	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if len(res.stderr) != 0 {
		t.Errorf("FAS_EXPLAIN unset must keep stderr empty; got %q", res.stderr)
	}
}

// TestRun_FasExplain_TruthyVariants is the table-driven case for the truthy
// set: 1, true, yes, TRUE (case-insensitive). Each must enable the env
// fallback. Using distinct tempdirs per subtest prevents rule-cache crosstalk.
func TestRun_FasExplain_TruthyVariants(t *testing.T) {
	// Parent-level guard: a host-exported FAS_EXPLAIN must not bleed into
	// the first subtest before its own t.Setenv runs.
	t.Setenv("FAS_EXPLAIN", "")

	cases := []struct {
		name string
		val  string
	}{
		{"digit_one", "1"},
		{"true_lower", "true"},
		{"yes_lower", "yes"},
		{"true_upper", "TRUE"},
		{"yes_upper", "YES"},
		{"true_mixed", "True"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAS_EXPLAIN", tc.val)

			projectDir := writeRuleFiles(t, map[string]string{
				"flags-miss.cue": missingKeyRule,
			})
			globalDir := emptyRulesDir(t)

			stdin := claudeBashInput("ls")
			res := runCLI(t, stdin,
				"eval",
				"--harness", "claude",
				"--config", projectDir,
				"--global-config", globalDir,
			)

			if res.exit != 0 {
				t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
			}
			if !strings.Contains(string(res.stderr), "E0201") {
				t.Errorf("FAS_EXPLAIN=%q must enable missed diagnostics (E0201); stderr=%q",
					tc.val, res.stderr)
			}
		})
	}
}

// TestRun_FasExplain_NonTruthyVariants is the table-driven case for non-truthy
// values. Each must leave the fallback off, producing empty stderr. Guards
// against sloppy truthiness logic that might treat any non-empty value as on,
// or lowercase-only matching that would miss TRUE/YES but accept "bogus".
//
// The filter-mode words ("fired", "missed", "both") are included as NON-truthy
// to pin that the env var is a truthiness flag, NOT a filter-mode channel — a
// naive implementation that reused --explain's Set() parser on $FAS_EXPLAIN
// would wrongly treat these as valid filter modes and enable explain.
func TestRun_FasExplain_NonTruthyVariants(t *testing.T) {
	// Parent-level guard: a host-exported FAS_EXPLAIN must not bleed into
	// the first subtest before its own t.Setenv runs.
	t.Setenv("FAS_EXPLAIN", "")

	cases := []struct {
		name string
		val  string
	}{
		{"digit_zero", "0"},
		{"false_lower", "false"},
		{"no_lower", "no"},
		{"empty", ""},
		{"arbitrary", "bogus"},
		{"false_upper", "FALSE"},
		{"no_upper", "NO"},
		{"filter_word_fired", "fired"},
		{"filter_word_missed", "missed"},
		{"filter_word_both", "both"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAS_EXPLAIN", tc.val)

			projectDir := writeRuleFiles(t, map[string]string{
				"flags-miss.cue": missingKeyRule,
			})
			globalDir := emptyRulesDir(t)

			stdin := claudeBashInput("ls")
			res := runCLI(t, stdin,
				"eval",
				"--harness", "claude",
				"--config", projectDir,
				"--global-config", globalDir,
			)

			if res.exit != 0 {
				t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
			}
			if len(res.stderr) != 0 {
				t.Errorf("FAS_EXPLAIN=%q must leave explain disabled; got stderr=%q",
					tc.val, res.stderr)
			}
		})
	}
}

// normalizeTempPaths replaces occurrences of the project and global tempdirs
// in a byte slice with stable placeholders so byte-level comparisons across
// runs (each with its own t.TempDir()) are not trivially broken by distinct
// path prefixes.
func normalizeTempPaths(b []byte, projectDir, globalDir string) []byte {
	out := bytes.ReplaceAll(b, []byte(projectDir), []byte("<PROJECT>"))
	out = bytes.ReplaceAll(out, []byte(globalDir), []byte("<GLOBAL>"))
	return out
}

// TestRun_FasExplain_EquivalentToMissedFlag is the H1 equivalence anchor:
// FAS_EXPLAIN=1 and --explain=missed must produce byte-identical stderr
// given identical input and rule contents. Tempdir paths (project AND global)
// are normalized out before the comparison so the two runs' distinct
// t.TempDir()s don't trivially diverge on path prefix.
//
// This is the env-first, then flag-first ordering. Its sibling
// TestRun_FasExplain_EquivalentToMissedFlag_ReverseOrder swaps the order to
// prove symmetry — neither ordering should be able to mask a sticky-toggle
// bug in the evaluator's package-level state.
func TestRun_FasExplain_EquivalentToMissedFlag(t *testing.T) {
	stdin := claudeBashInput("ls")

	// Run A: env-driven.
	envProjectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	envGlobalDir := emptyRulesDir(t)
	t.Setenv("FAS_EXPLAIN", "1")
	envRun := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", envProjectDir,
		"--global-config", envGlobalDir,
	)
	if envRun.exit != 0 {
		t.Fatalf("env run exit=%d want 0; stderr=%s", envRun.exit, envRun.stderr)
	}

	// Run B: flag-driven — unset env here so the comparison is strictly
	// env-only vs flag-only.
	t.Setenv("FAS_EXPLAIN", "")
	flagProjectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	flagGlobalDir := emptyRulesDir(t)
	flagRun := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", flagProjectDir,
		"--global-config", flagGlobalDir,
		"--explain=missed",
	)
	if flagRun.exit != 0 {
		t.Fatalf("flag run exit=%d want 0; stderr=%s", flagRun.exit, flagRun.stderr)
	}

	envNorm := normalizeTempPaths(envRun.stderr, envProjectDir, envGlobalDir)
	flagNorm := normalizeTempPaths(flagRun.stderr, flagProjectDir, flagGlobalDir)
	if !bytes.Equal(envNorm, flagNorm) {
		t.Errorf("FAS_EXPLAIN=1 stderr must match --explain=missed stderr;\n env=%s\n flag=%s",
			envNorm, flagNorm)
	}

	// Sanity: both must have emitted E0201.
	if !strings.Contains(string(envRun.stderr), "E0201") {
		t.Errorf("env run should carry E0201; stderr=%q", envRun.stderr)
	}
	if !strings.Contains(string(flagRun.stderr), "E0201") {
		t.Errorf("flag run should carry E0201; stderr=%q", flagRun.stderr)
	}
}

// TestRun_FasExplain_EquivalentToMissedFlag_ReverseOrder swaps the execution
// order of the equivalence test: flag FIRST, then env-only. Proves symmetry.
// If the evaluator had a sticky-toggle bug (e.g. explainToggle latches once
// set and never resets), the original env-first ordering could mask it —
// a flag-driven second run would re-enable via flag and hide the leak. This
// variant's second run relies solely on env-driven enablement, so any sticky
// state from the first run cannot paper over an env-path regression.
func TestRun_FasExplain_EquivalentToMissedFlag_ReverseOrder(t *testing.T) {
	stdin := claudeBashInput("ls")

	// Run A: flag-driven. Env explicitly cleared so enablement is flag-only.
	t.Setenv("FAS_EXPLAIN", "")
	flagProjectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	flagGlobalDir := emptyRulesDir(t)
	flagRun := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", flagProjectDir,
		"--global-config", flagGlobalDir,
		"--explain=missed",
	)
	if flagRun.exit != 0 {
		t.Fatalf("flag run exit=%d want 0; stderr=%s", flagRun.exit, flagRun.stderr)
	}

	// Run B: env-driven. No flag — enablement must come purely from env.
	envProjectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	envGlobalDir := emptyRulesDir(t)
	t.Setenv("FAS_EXPLAIN", "1")
	envRun := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", envProjectDir,
		"--global-config", envGlobalDir,
	)
	if envRun.exit != 0 {
		t.Fatalf("env run exit=%d want 0; stderr=%s", envRun.exit, envRun.stderr)
	}

	flagNorm := normalizeTempPaths(flagRun.stderr, flagProjectDir, flagGlobalDir)
	envNorm := normalizeTempPaths(envRun.stderr, envProjectDir, envGlobalDir)
	if !bytes.Equal(flagNorm, envNorm) {
		t.Errorf("reverse-order: --explain=missed stderr must match FAS_EXPLAIN=1 stderr;\n flag=%s\n env=%s",
			flagNorm, envNorm)
	}
	if !strings.Contains(string(flagRun.stderr), "E0201") {
		t.Errorf("flag run should carry E0201; stderr=%q", flagRun.stderr)
	}
	if !strings.Contains(string(envRun.stderr), "E0201") {
		t.Errorf("env run should carry E0201; stderr=%q", envRun.stderr)
	}
}

// TestRun_FasExplain_FlagMissedWinsOverEnv is the H2 mirror to
// TestRun_FasExplain_FlagWinsOverEnv: --explain=missed combined with
// FAS_EXPLAIN=1 must produce missed-only output (no fired trace). A bug
// where env-driven enablement widens a restrictive flag (e.g. env OR flag
// instead of flag-wins) would leak the fired rule_id into stderr.
func TestRun_FasExplain_FlagMissedWinsOverEnv(t *testing.T) {
	t.Setenv("FAS_EXPLAIN", "1")

	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue":        askOnBashRule,
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain=missed",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	stderrStr := string(res.stderr)
	// Missed E-code must appear.
	if !strings.Contains(stderrStr, "E0201") {
		t.Errorf("--explain=missed must include missed E0201; stderr=%q", stderrStr)
	}
	// Fired rule_id must NOT appear. If env widened the filter to `both`
	// (or env's enablement OR'd with flag's filter to yield fired output),
	// this would trip.
	if strings.Contains(stderrStr, "ask-bash") {
		t.Errorf("--explain=missed with FAS_EXPLAIN=1 must suppress fired traces (flag wins); got `ask-bash` in stderr=%q",
			stderrStr)
	}
}

// TestRun_FasExplain_EnvResetsBetweenRuns mirrors T7's ToggleResetsBetweenRuns
// for the env-driven path: a first run enables explain via FAS_EXPLAIN=1, a
// second run in the same process with env cleared (and no flag) must produce
// empty stderr. Guards against env-driven enablement leaking into subsequent
// run() invocations as sticky state (e.g. if the implementation reads
// FAS_EXPLAIN once into a package-level latch and never clears it on
// subsequent calls where the env is no longer truthy).
func TestRun_FasExplain_EnvResetsBetweenRuns(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)
	stdin := claudeBashInput("ls")

	// First run: FAS_EXPLAIN=1 → stderr should carry a diagnostic.
	t.Setenv("FAS_EXPLAIN", "1")
	first := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if first.exit != 0 {
		t.Fatalf("first run exit=%d want 0; stderr=%s", first.exit, first.stderr)
	}
	if len(first.stderr) == 0 {
		t.Fatalf("first run (FAS_EXPLAIN=1) should emit diagnostics; got empty stderr")
	}

	// Second run: env cleared, no flag → stderr must be empty. If the CLI
	// latched enablement from the first run's env and never reset, localize
	// would keep firing here.
	t.Setenv("FAS_EXPLAIN", "")
	second := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if second.exit != 0 {
		t.Fatalf("second run exit=%d want 0; stderr=%s", second.exit, second.stderr)
	}
	if len(second.stderr) != 0 {
		t.Errorf("second run (FAS_EXPLAIN cleared, no flag) must have empty stderr; env-leak detected. stderr=%q",
			second.stderr)
	}

	// Third run: still env cleared, still no flag. Makes the assertion robust
	// against a future impl that only flips the toggle on the first couple of
	// calls.
	third := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if third.exit != 0 {
		t.Fatalf("third run exit=%d want 0; stderr=%s", third.exit, third.stderr)
	}
	if len(third.stderr) != 0 {
		t.Errorf("third run (FAS_EXPLAIN cleared, no flag) must have empty stderr; env-leak detected. stderr=%q",
			third.stderr)
	}
}

// -----------------------------------------------------------------------------
// fas explain <rule_id> subcommand (T9)
// -----------------------------------------------------------------------------
//
// `fas explain <rule_id> < input.json` runs ONE named rule against the stdin
// input. Unlike `fas eval`, the subcommand uses the exit code to encode
// match/no-match:
//
//   exit 0 → rule matched; stderr empty; stdout empty (no --render yet)
//   exit 1 → rule did not match; diagnostic on stderr (implicit explain on)
//   exit 2 → engine error (unknown rule_id, bad stdin JSON, missing arg, etc.)
//
// Resolution: rule_id is looked up across both global and project rule sets.
// When the same rule_id exists in both, project wins (narrower scope
// overrides global, matching the two-phase override semantics). The
// implementer should document this choice.

// explainCmdRuleGlobalOnly is a match-on-Bash rule with rule_id `global-only`.
// Used to assert `fas explain` resolves rule_id against the global set.
const explainCmdRuleGlobalOnly = `package rules

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
	}
	then: ask: {
		rule_id:  "global-only"
		reason:   "global fires"
		question: "proceed?"
	}
}
`

// explainCmdRuleProjectOnly mirrors the shape of the global-only fixture but
// lives only in the project set and has rule_id `project-only`.
const explainCmdRuleProjectOnly = `package rules

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
	}
	then: ask: {
		rule_id:  "project-only"
		reason:   "project fires"
		question: "proceed?"
	}
}
`

// explainCmdRuleAmbiguousGlobal and explainCmdRuleAmbiguousProject share a
// rule_id (`shared-id`) but distinct reasons so a test can tell which ruleset
// was chosen when both sets contain the same id.
const explainCmdRuleAmbiguousGlobal = `package rules

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
	}
	then: ask: {
		rule_id:  "shared-id"
		reason:   "from-global"
		question: "proceed?"
	}
}
`

// TestRun_ExplainCmd_MatchingRule_ExitZero is the positive path: a matching
// rule resolves by id, the subcommand returns exit 0, and stderr stays empty.
// stdout is empty for T9 (no --render flag); see scope.md line 316 for the stdout contract.
func TestRun_ExplainCmd_MatchingRule_ExitZero(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue": askOnBashRule,
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, claudeBashInput("ls"),
		"explain", "ask-bash",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s stdout=%s", res.exit, res.stderr, res.stdout)
	}
	if len(res.stderr) != 0 {
		t.Errorf("matching rule: stderr must be empty; got %q", res.stderr)
	}
	if len(res.stdout) != 0 {
		t.Errorf("matching rule without --render: stdout must be empty; got %q", res.stdout)
	}
}

// TestRun_ExplainCmd_NonMatchingRule_ExitOne covers the no-match branch: the
// rule exists but its `when` doesn't subsume the input. Exit code 1, and a
// diagnostic (E-code + file:line:col) must appear on stderr — the subcommand
// has implicit explain-on behavior regardless of any --explain flag.
func TestRun_ExplainCmd_NonMatchingRule_ExitOne(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, claudeBashInput("ls"),
		"explain", "flags-rule",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 1 {
		t.Fatalf("exit=%d want 1 (no-match); stderr=%s stdout=%s",
			res.exit, res.stderr, res.stdout)
	}
	if len(res.stderr) == 0 {
		t.Fatalf("no-match: stderr must carry a diagnostic, got empty")
	}
	if got := strings.Count(string(res.stderr), "E0201"); got != 1 {
		t.Errorf("want exactly one E0201 in stderr, got %d; stderr=%q", got, res.stderr)
	}
	if _, _, ok := parseExplainLineCol(res.stderr); !ok {
		t.Errorf("no-match stderr must carry `<file>.cue:<line>:<col>` position anchor; got %q",
			res.stderr)
	}
}

// TestRun_ExplainCmd_MissingRuleID_ExitTwo covers the engine-error branch for
// an unknown rule_id. Exit 2 and stderr must name the rule_id that was not
// found. The error is NOT a diagnostic (no E-code expected) — it's a CLI
// resolution error.
func TestRun_ExplainCmd_MissingRuleID_ExitTwo(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue": askOnBashRule,
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, claudeBashInput("ls"),
		"explain", "does-not-exist",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 2 {
		t.Fatalf("exit=%d want 2 (engine error); stderr=%s", res.exit, res.stderr)
	}
	if !strings.Contains(string(res.stderr), "does-not-exist") {
		t.Errorf("stderr must name the unresolved rule_id `does-not-exist`; got %q", res.stderr)
	}
	if strings.Contains(string(res.stderr), "E02") || strings.Contains(string(res.stderr), "error[E") {
		t.Errorf("missing rule_id is an engine error, must not emit a localize diagnostic; stderr=%q", res.stderr)
	}
}

// TestRun_ExplainCmd_NoRuleIDArg_ExitTwo asserts the subcommand refuses to run
// without a rule_id positional arg. Must NOT silently pick the first rule.
func TestRun_ExplainCmd_NoRuleIDArg_ExitTwo(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue": askOnBashRule,
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, claudeBashInput("ls"),
		"explain",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 2 {
		t.Fatalf("exit=%d want 2 (usage error); stderr=%s stdout=%s",
			res.exit, res.stderr, res.stdout)
	}
	if len(res.stderr) == 0 {
		t.Fatalf("missing rule_id must print a usage diagnostic on stderr; got empty")
	}
	// Positive: usage-style wording must name the missing arg.
	stderrStr := string(res.stderr)
	if !strings.Contains(stderrStr, "rule_id") && !strings.Contains(stderrStr, "usage") {
		t.Errorf("stderr must mention `rule_id` or `usage`; got %q", res.stderr)
	}
	// Negative: a silent fallthrough that picked the first rule would emit an
	// E-code (localize diagnostic) or reference an existing fixture rule_id.
	if strings.Contains(stderrStr, "error[E") || strings.Contains(stderrStr, "E02") {
		t.Errorf("no-arg must be a usage error, not a localize diagnostic; stderr=%q", res.stderr)
	}
	if strings.Contains(stderrStr, "ask-bash") {
		t.Errorf("no-arg must not silently execute the first rule (found fixture rule_id `ask-bash` in stderr); stderr=%q", res.stderr)
	}
}

// TestRun_ExplainCmd_MalformedStdin_ExitTwo covers the engine-error branch
// for malformed stdin JSON. The subcommand cannot produce a cue.Value from
// "not json" and must exit 2 with a parse/decode diagnostic on stderr.
func TestRun_ExplainCmd_MalformedStdin_ExitTwo(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"ask.cue": askOnBashRule,
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, []byte("not json"),
		"explain", "ask-bash",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 2 {
		t.Fatalf("exit=%d want 2 (engine error on malformed stdin); stderr=%s",
			res.exit, res.stderr)
	}
	if len(res.stderr) == 0 {
		t.Errorf("malformed stdin must surface an error on stderr; got empty")
	}
}

// TestRun_ExplainCmd_DiagnosticPrintedWithoutExplainFlag pins the implicit
// explain-on contract: the subcommand always renders its no-match diagnostic,
// even without --explain. Contrast with `fas eval` where the absent flag
// yields empty stderr on non-match.
func TestRun_ExplainCmd_DiagnosticPrintedWithoutExplainFlag(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	// No --explain flag anywhere in args.
	res := runCLI(t, claudeBashInput("ls"),
		"explain", "flags-rule",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 1 {
		t.Fatalf("exit=%d want 1 (no-match); stderr=%s", res.exit, res.stderr)
	}
	if len(res.stderr) == 0 {
		t.Fatalf("explain subcmd must print diagnostic on no-match even without --explain; stderr empty")
	}
	if got := strings.Count(string(res.stderr), "E0201"); got != 1 {
		t.Errorf("want exactly one E0201 in stderr, got %d; stderr=%q", got, res.stderr)
	}
}

// TestRun_ExplainCmd_ResolvesGlobalRuleID asserts rule_id resolution walks the
// global rules dir. A rule living in globalDir only must still be findable.
// Pinned by BOTH exit 0 and empty stdout — explain subcmd doesn't emit a
// vendor envelope, so a raw eval-path fallthrough would fail the stdout check.
func TestRun_ExplainCmd_ResolvesGlobalRuleID(t *testing.T) {
	projectDir := emptyRulesDir(t)
	globalDir := writeRuleFiles(t, map[string]string{
		"global.cue": explainCmdRuleGlobalOnly,
	})

	res := runCLI(t, claudeBashInput("ls"),
		"explain", "global-only",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0 (rule in global resolves + matches); stderr=%s",
			res.exit, res.stderr)
	}
	if len(res.stderr) != 0 {
		t.Errorf("match from global rule: stderr must be empty; got %q", res.stderr)
	}
	// Anchor: explain subcmd must NOT emit the vendor envelope on stdout.
	// A raw eval-path fallthrough would pollute stdout with JSON.
	if len(res.stdout) != 0 {
		t.Errorf("explain subcmd (match): stdout must be empty; got %q", res.stdout)
	}
}

// TestRun_ExplainCmd_ResolvesProjectRuleID is the symmetric test: rule in
// projectDir only must resolve.
func TestRun_ExplainCmd_ResolvesProjectRuleID(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"project.cue": explainCmdRuleProjectOnly,
	})
	globalDir := emptyRulesDir(t)

	res := runCLI(t, claudeBashInput("ls"),
		"explain", "project-only",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0 (rule in project resolves + matches); stderr=%s",
			res.exit, res.stderr)
	}
	if len(res.stderr) != 0 {
		t.Errorf("match from project rule: stderr must be empty; got %q", res.stderr)
	}
	if len(res.stdout) != 0 {
		t.Errorf("explain subcmd (match): stdout must be empty; got %q", res.stdout)
	}
}

// TestRun_ExplainCmd_ResolvesFromEitherSide_BothPopulated proves the resolver
// walks BOTH rule sets when BOTH are populated with distinct ids. The single-
// sided cases above could pass if the resolver only visited whichever set is
// non-empty; this pins that both are always searched.
func TestRun_ExplainCmd_ResolvesFromEitherSide_BothPopulated(t *testing.T) {
	globalDir := writeRuleFiles(t, map[string]string{
		"global.cue": strings.ReplaceAll(explainCmdRuleGlobalOnly, `"global-only"`, `"global-side"`),
	})
	projectDir := writeRuleFiles(t, map[string]string{
		"project.cue": strings.ReplaceAll(explainCmdRuleProjectOnly, `"project-only"`, `"project-side"`),
	})

	first := runCLI(t, claudeBashInput("ls"),
		"explain", "global-side",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if first.exit != 0 {
		t.Fatalf("global-side: exit=%d want 0; stderr=%s", first.exit, first.stderr)
	}
	if len(first.stderr) != 0 {
		t.Errorf("global-side match: stderr must be empty; got %q", first.stderr)
	}
	// Anchor the subcmd contract: no eval-path fallthrough (empty stdout, no envelope).
	if len(first.stdout) != 0 {
		t.Errorf("global-side match: stdout must be empty (no vendor envelope); got %q", first.stdout)
	}

	second := runCLI(t, claudeBashInput("ls"),
		"explain", "project-side",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if second.exit != 0 {
		t.Fatalf("project-side: exit=%d want 0; stderr=%s", second.exit, second.stderr)
	}
	if len(second.stderr) != 0 {
		t.Errorf("project-side match: stderr must be empty; got %q", second.stderr)
	}
	if len(second.stdout) != 0 {
		t.Errorf("project-side match: stdout must be empty (no vendor envelope); got %q", second.stdout)
	}
}

// TestRun_ExplainCmd_AmbiguousRuleID_ProjectWins pins the tie-break: when the
// same rule_id lives in both global and project sets, the project rule is
// selected. Matches the two-phase override semantics where project effects
// override global. Implementer: document this choice.
//
// To distinguish which rule ran, both fixtures match the same input (so exit
// code alone can't tell them apart) but carry distinct reasons. Since T9's
// stdout is empty on match, we can't probe the reason directly — instead we
// make the project rule NOT match (swap a field) and assert exit 1 proving
// project was chosen, while global would have matched.
func TestRun_ExplainCmd_AmbiguousRuleID_ProjectWins(t *testing.T) {
	// Project's shared-id rule requires flags.force (never present) → would
	// NOT match a plain Bash payload. Global's shared-id rule matches any
	// Bash payload. If project wins, exit 1 (project didn't match). If
	// global wins, exit 0.
	projectDir := writeRuleFiles(t, map[string]string{
		"shared.cue": strings.ReplaceAll(missingKeyRule, `"flags-rule"`, `"shared-id"`),
	})
	globalDir := writeRuleFiles(t, map[string]string{
		"shared.cue": explainCmdRuleAmbiguousGlobal,
	})

	res := runCLI(t, claudeBashInput("ls"),
		"explain", "shared-id",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 1 {
		t.Fatalf("exit=%d want 1 (project `shared-id` misses, proving project wins over global); stderr=%s",
			res.exit, res.stderr)
	}
	if len(res.stderr) == 0 {
		t.Errorf("ambiguous-id project-wins no-match: stderr must carry diagnostic; got empty")
	}
}

// TestRun_ExplainCmd_DenyRule_StillExitsZeroOnMatch pins the contract boundary
// between T7's --explain (which never flips exit code — eval decision stands)
// and T9's explain subcommand (which DOES use exit code for match/no-match).
// A deny-rule that matches the input yields exit 0 from `fas explain` — the
// match succeeded; the deny decision is irrelevant to the subcommand's exit.
func TestRun_ExplainCmd_DenyRule_StillExitsZeroOnMatch(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"system.cue": denySystemTargetRule,
	})
	globalDir := emptyRulesDir(t)

	// denySystemTargetRule's rule_id is "r1"; input matches (system target).
	res := runCLI(t, claudeBashInput("rm -rf /etc/passwd"),
		"explain", "r1",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0 (deny rule matched = match, not an error); stderr=%s",
			res.exit, res.stderr)
	}
	if len(res.stderr) != 0 {
		t.Errorf("deny-rule match: stderr must be empty; got %q", res.stderr)
	}
	// Anchor the subcommand contract: stdout must be empty (no vendor envelope).
	// This also distinguishes the subcmd path from a raw eval-path fallthrough
	// that would print a deny envelope to stdout.
	if len(res.stdout) != 0 {
		t.Errorf("explain subcmd (match): stdout must be empty; got %q", res.stdout)
	}
}

// TestRun_ExplainCmd_DoesNotLeakToggleIntoEval is the leak guard: the explain
// subcommand flips the evaluator's package-level explain toggle on internally
// (to render diagnostics on no-match). A subsequent `fas eval` without
// --explain in the same process must NOT see leaked diagnostics on stderr.
// Mirrors TestRun_Explain_ToggleResetsBetweenRuns for the subcommand entry.
func TestRun_ExplainCmd_DoesNotLeakToggleIntoEval(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"flags-miss.cue": missingKeyRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")

	// First: run the explain subcommand on a non-matching rule. Toggle is
	// set internally for localization; stderr should carry the diagnostic.
	first := runCLI(t, stdin,
		"explain", "flags-rule",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if first.exit != 1 {
		t.Fatalf("first (explain subcmd) exit=%d want 1; stderr=%s", first.exit, first.stderr)
	}
	if len(first.stderr) == 0 {
		t.Fatalf("first (explain subcmd) should emit diagnostics; got empty stderr")
	}

	// Second: run `fas eval` without --explain. Stderr must stay empty —
	// otherwise the subcommand leaked its toggle into the eval path.
	second := runCLI(t, stdin,
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if second.exit != 0 {
		t.Fatalf("second (eval no-flag) exit=%d want 0; stderr=%s", second.exit, second.stderr)
	}
	if len(second.stderr) != 0 {
		t.Errorf("explain subcommand leaked evaluator toggle into subsequent eval; stderr=%q",
			second.stderr)
	}

	// Third: positive control — run `fas eval --explain=missed` on the same
	// rule set. Stderr MUST be non-empty, proving the eval path is capable of
	// emitting diagnostics when the flag is set. If run 2 was empty because of
	// a broken pipeline (not a sealed toggle), this run would also be empty.
	third := runCLI(t, stdin,
		"eval",
		"--explain=missed",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if third.exit != 0 {
		t.Fatalf("third (eval --explain=missed) exit=%d want 0; stderr=%s", third.exit, third.stderr)
	}
	if len(third.stderr) == 0 {
		t.Errorf("positive control: eval --explain=missed must emit diagnostics, got empty stderr (run 2's emptiness is not meaningful)")
	}
}

// -----------------------------------------------------------------------------
// T10: `fas explain --code <code>` — offline help lookup
// -----------------------------------------------------------------------------

// stdinMustNotBeRead is a reader whose Read always fails. Passing it as stdin
// proves the --code fast-path never consults stdin.
type stdinMustNotBeRead struct {
	t *testing.T
}

func (s stdinMustNotBeRead) Read(_ []byte) (int, error) {
	s.t.Fatalf("--code path must not read stdin")
	return 0, nil
}

// TestRun_ExplainCode_ValidCode_ExitZero pins the positive path: `--code E0201`
// prints the registered help to stdout and exits 0 with empty stderr.
func TestRun_ExplainCode_ValidCode_ExitZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := run(stdinMustNotBeRead{t: t}, &stdout, &stderr,
		[]string{"explain", "--code", "E0201"})

	if exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s stdout=%s", exit, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr must be empty on valid --code; got %q", stderr.String())
	}
	want := "A path segment referenced in the rule does not exist in the input."
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout must contain E0201 help phrase %q; got %q", want, stdout.String())
	}
	if !strings.HasSuffix(stdout.String(), "\n") {
		t.Errorf("stdout must end with a trailing newline; got %q", stdout.String())
	}
}

// TestRun_ExplainCode_EqualsForm accepts `--code=E0301`, the equivalent flag
// spelling. Separate case because flag.Parse treats the two forms differently.
func TestRun_ExplainCode_EqualsForm(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := run(stdinMustNotBeRead{t: t}, &stdout, &stderr,
		[]string{"explain", "--code=E0301"})

	if exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", exit, stderr.String())
	}
	want := "A string leaf does not satisfy the regex declared in the rule."
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout must contain E0301 help phrase %q; got %q", want, stdout.String())
	}
}

// TestRun_ExplainCode_UnknownCode_ExitTwo pins the error branch: an unregistered
// code surfaces on stderr, stdout stays empty, exit code is 2.
func TestRun_ExplainCode_UnknownCode_ExitTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := run(stdinMustNotBeRead{t: t}, &stdout, &stderr,
		[]string{"explain", "--code", "E9999"})

	if exit != 2 {
		t.Fatalf("exit=%d want 2; stderr=%s stdout=%s", exit, stderr.String(), stdout.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("unknown --code: stdout must be empty; got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "E9999") {
		t.Errorf("stderr must name the unknown code `E9999`; got %q", stderr.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "unknown") {
		t.Errorf("stderr must indicate the code is unknown; got %q", stderr.String())
	}
}

// TestRun_ExplainCode_CaseSensitive asserts the lookup is case-sensitive per
// LookupCode's documented contract. `e0201` must not resolve to E0201.
func TestRun_ExplainCode_CaseSensitive(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := run(stdinMustNotBeRead{t: t}, &stdout, &stderr,
		[]string{"explain", "--code", "e0201"})

	if exit != 2 {
		t.Fatalf("exit=%d want 2 (lowercase is not a valid code); stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("case mismatch: stdout must be empty; got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "e0201") {
		t.Errorf("stderr must name the invalid code `e0201`; got %q", stderr.String())
	}
}

// TestRun_ExplainCode_DoesNotLoadRules pins the fast-path contract: when
// `--code` is supplied, rule loading is bypassed entirely. The test points
// both --config and --global-config at regular files (not directories); the
// loader rejects "not a directory" paths with a real error (unlike missing
// paths, which it silently treats as empty), so a naive implementation that
// calls loadRulesDir before branching on --code would surface that error on
// stderr and exit non-zero. A correct --code fast-path stays exit 0 with empty
// stderr.
func TestRun_ExplainCode_DoesNotLoadRules(t *testing.T) {
	dir := t.TempDir()
	projectFile := filepath.Join(dir, "project-not-a-dir")
	globalFile := filepath.Join(dir, "global-not-a-dir")
	if err := os.WriteFile(projectFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write project sentinel: %v", err)
	}
	if err := os.WriteFile(globalFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write global sentinel: %v", err)
	}

	var stdout, stderr bytes.Buffer
	exit := run(stdinMustNotBeRead{t: t}, &stdout, &stderr,
		[]string{
			"explain", "--code", "E0201",
			"--config", projectFile,
			"--global-config", globalFile,
		})

	if exit != 0 {
		t.Fatalf("exit=%d want 0 (--code must not load rules); stderr=%s", exit, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("--code path must not touch rule loader; got stderr=%q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "path") {
		t.Errorf("stdout must carry the E0201 help (contains %q); got %q", "path", stdout.String())
	}
}

// TestRun_ExplainCode_EmptyValue pins that an empty --code value is rejected
// with exit 2 and stderr names the invalid input using the shared "unknown"
// vocabulary (consistent with the UnknownCode case).
func TestRun_ExplainCode_EmptyValue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := run(stdinMustNotBeRead{t: t}, &stdout, &stderr,
		[]string{"explain", "--code", ""})

	if exit != 2 {
		t.Fatalf("exit=%d want 2 (empty --code is invalid); stderr=%s stdout=%s",
			exit, stderr.String(), stdout.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("empty --code: stdout must be empty; got %q", stdout.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "unknown") {
		t.Errorf("stderr must flag the empty code as unknown; got %q", stderr.String())
	}
}

// TestRun_ExplainCode_PrecedenceOverRuleID pins the precedence contract: if
// `--code` appears anywhere in args, it wins — any positional rule_id is
// ignored, stdin is not read, and rule loading is bypassed. This is the
// cleanest resolution of the ambiguity between runExplain's current
// args[0]-as-rule_id consumption and the new flag.
func TestRun_ExplainCode_PrecedenceOverRuleID(t *testing.T) {
	dir := t.TempDir()
	bogus := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(bogus, []byte("x"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	var stdout, stderr bytes.Buffer
	exit := run(stdinMustNotBeRead{t: t}, &stdout, &stderr,
		[]string{
			"explain", "some_rule", "--code", "E0201",
			"--config", bogus,
			"--global-config", bogus,
		})

	if exit != 0 {
		t.Fatalf("exit=%d want 0 (--code takes precedence over rule_id); stderr=%s stdout=%s",
			exit, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("precedence: stderr must be empty; got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "E0201") {
		t.Errorf("stdout must reference the resolved code %q; got %q", "E0201", stdout.String())
	}
	if !strings.Contains(stdout.String(), "path") {
		t.Errorf("stdout must carry the E0201 help (contains %q); got %q", "path", stdout.String())
	}
}

// -----------------------------------------------------------------------------
// fas vet — standalone rule validation
// -----------------------------------------------------------------------------

func TestRun_Vet_ValidRules_ExitZero(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"system.cue": denySystemTargetRule,
	})
	globalDir := writeRuleFiles(t, map[string]string{
		"ask.cue": askOnBashRule,
	})

	var stdout, stderr bytes.Buffer
	exit := run(nil, &stdout, &stderr,
		[]string{"vet",
			"--config", projectDir,
			"--global-config", globalDir,
		})

	if exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", exit, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "ok:") {
		t.Errorf("expected ok summary; stdout=%q", out)
	}
	if !strings.Contains(out, "r1") {
		t.Errorf("expected rule_id r1 in summary; stdout=%q", out)
	}
	if !strings.Contains(out, "ask-bash") {
		t.Errorf("expected rule_id ask-bash in summary; stdout=%q", out)
	}
}

func TestRun_Vet_EmptyDirs_ExitZero(t *testing.T) {
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	var stdout, stderr bytes.Buffer
	exit := run(nil, &stdout, &stderr,
		[]string{"vet",
			"--config", projectDir,
			"--global-config", globalDir,
		})

	if exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "0 rules") {
		t.Errorf("expected 0 rules; stdout=%q", stdout.String())
	}
}

func TestRun_Vet_MalformedRule_ExitOne(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"bad.cue": malformedRuleSrc,
	})
	globalDir := emptyRulesDir(t)

	var stdout, stderr bytes.Buffer
	exit := run(nil, &stdout, &stderr,
		[]string{"vet",
			"--config", projectDir,
			"--global-config", globalDir,
		})

	if exit != 1 {
		t.Fatalf("exit=%d want 1; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
}

func TestRun_Vet_ModifyRuleClaudeAdapter_ExitOne(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"mod.cue": modifyRuleSrc,
	})
	globalDir := emptyRulesDir(t)

	var stdout, stderr bytes.Buffer
	exit := run(nil, &stdout, &stderr,
		[]string{"vet",
			"--harness", "claude",
			"--config", projectDir,
			"--global-config", globalDir,
		})

	if exit != 1 {
		t.Fatalf("exit=%d want 1; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "modify") {
		t.Errorf("expected modify capability error; stderr=%q", stderr.String())
	}
}

func TestRun_Vet_UnknownHarness_ExitTwo(t *testing.T) {
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	var stdout, stderr bytes.Buffer
	exit := run(nil, &stdout, &stderr,
		[]string{"vet",
			"--harness", "unknown",
			"--config", projectDir,
			"--global-config", globalDir,
		})

	if exit != 2 {
		t.Fatalf("exit=%d want 2; stderr=%s", exit, stderr.String())
	}
}

func TestRun_Vet_FormatJSON_ExitZero(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"system.cue": denySystemTargetRule,
	})
	globalDir := emptyRulesDir(t)

	var stdout, stderr bytes.Buffer
	exit := run(nil, &stdout, &stderr,
		[]string{"vet",
			"--format", "json",
			"--config", projectDir,
			"--global-config", globalDir,
		})

	if exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", exit, stderr.String())
	}
	var summary struct {
		Status       string   `json:"status"`
		GlobalRules  []string `json:"global_rules"`
		ProjectRules []string `json:"project_rules"`
		Total        int      `json:"total"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("invalid JSON summary: %v\nstdout=%s", err, stdout.String())
	}
	if summary.Status != "ok" {
		t.Errorf("status=%q, want ok", summary.Status)
	}
	if summary.Total != 1 {
		t.Errorf("total=%d, want 1", summary.Total)
	}
}

func TestRun_Vet_NoStdinNeeded(t *testing.T) {
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	var stdout, stderr bytes.Buffer
	exit := run(nil, &stdout, &stderr,
		[]string{"vet",
			"--config", projectDir,
			"--global-config", globalDir,
		})

	if exit != 0 {
		t.Fatalf("exit=%d want 0 (nil stdin must be fine); stderr=%s", exit, stderr.String())
	}
}

func TestRun_Vet_DuplicateRuleName_ExitOne(t *testing.T) {
	rule1 := `package rules
dup_rule: {
	when: hook_event_name: "PreToolUse"
	then: deny: { rule_id: "dup", reason: "first" }
}
`
	rule2 := `package rules
dup_rule: {
	when: hook_event_name: "PreToolUse"
	then: deny: { rule_id: "dup", reason: "second" }
}
`
	projectDir := writeRuleFiles(t, map[string]string{
		"a.cue": rule1,
		"b.cue": rule2,
	})
	globalDir := emptyRulesDir(t)

	var stdout, stderr bytes.Buffer
	exit := run(nil, &stdout, &stderr,
		[]string{"vet",
			"--config", projectDir,
			"--global-config", globalDir,
		})

	if exit != 1 {
		t.Fatalf("exit=%d want 1; stderr=%s", exit, stderr.String())
	}
}

// -----------------------------------------------------------------------------
// FAS_LOG — debug payload logging
// -----------------------------------------------------------------------------

func TestRun_FasLog_Disabled_NoFiles(t *testing.T) {
	t.Setenv("FAS_LOG", "")
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	_ = runCLI(t, stdin,
		"eval", "--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	// No assertion on files — just ensure no panic or error.
}

func TestRun_FasLog_WritesLogFile(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv("FAS_LOG", logDir)
	t.Setenv("FAS_LOG_TTL", "1h")

	projectDir := writeRuleFiles(t, map[string]string{
		"system.cue": denySystemTargetRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("rm -rf /etc/passwd")
	res := runCLI(t, stdin,
		"eval", "--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)
	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	var logFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			logFiles = append(logFiles, e)
		}
	}
	if len(logFiles) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(logFiles))
	}

	data, err := os.ReadFile(filepath.Join(logDir, logFiles[0].Name()))
	if err != nil {
		t.Fatal(err)
	}

	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("log file is not valid JSON: %v", err)
	}
	if _, ok := entry["timestamp"]; !ok {
		t.Error("log entry missing timestamp")
	}
	if _, ok := entry["raw_input"]; !ok {
		t.Error("log entry missing raw_input")
	}
	if _, ok := entry["output"]; !ok {
		t.Error("log entry missing output")
	}
	if code, ok := entry["exit_code"].(float64); !ok || code != 0 {
		t.Errorf("exit_code=%v, want 0", entry["exit_code"])
	}
	if rules, ok := entry["rules"].(map[string]any); ok {
		if _, ok := rules["project"]; !ok {
			t.Error("log entry missing rules.project")
		}
	} else {
		t.Error("log entry missing rules")
	}
}

func TestRun_FasLog_RecordsMatchesOnDeny(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv("FAS_LOG", logDir)
	t.Setenv("FAS_LOG_TTL", "1h")

	projectDir := writeRuleFiles(t, map[string]string{
		"system.cue": denySystemTargetRule,
	})
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("rm -rf /etc/passwd")
	_ = runCLI(t, stdin,
		"eval", "--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	entries, _ := os.ReadDir(logDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			data, _ := os.ReadFile(filepath.Join(logDir, e.Name()))
			var entry map[string]any
			_ = json.Unmarshal(data, &entry)
			matches, ok := entry["matches"].([]any)
			if !ok || len(matches) == 0 {
				t.Error("expected at least one match in log entry")
			}
		}
	}
}

func TestRun_FasLog_NonFatalOnBadDir(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAS_LOG", filepath.Join(notADir, "logs"))
	projectDir := emptyRulesDir(t)
	globalDir := emptyRulesDir(t)

	stdin := claudeBashInput("ls")
	res := runCLI(t, stdin,
		"eval", "--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0 (log failure must be non-fatal); stderr=%s", res.exit, res.stderr)
	}
	if !strings.Contains(string(res.stderr), "FAS_LOG") {
		t.Errorf("expected FAS_LOG warning on stderr, got %q", res.stderr)
	}
}
