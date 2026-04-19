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
// `import` the quae stdlib through LoadRules (single-file CompileBytes), so
// bodies either stay concrete or inline the stdlib shapes they depend on.
// -----------------------------------------------------------------------------

// denySystemTargetRule blocks Bash calls whose parsed targets look like a
// system path. Mirrors cue/quae.cue's #isPreToolUse & #isBash & #hasSystemTarget
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

rule: {
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
