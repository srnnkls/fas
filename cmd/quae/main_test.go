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

// missingKeyRule references a path segment (`tool_input.flags.force`) that a
// plain Bash payload — `claudeBashInput` emits only `tool_input.command` —
// does not expose. The rule therefore cannot match, and localize must yield
// an E0201 keyed on the absent `flags` label. The `flags` label sits on
// line 8 of the written file (1: `package rules`, 2: blank, 3: `rule: {`,
// 4: `\twhen: {`, 5: `\t\thook_event_name: "PreToolUse"`, 6: `\t\ttool_name:
// "Bash"`, 7: `\t\ttool_input: {`, 8: `\t\t\tflags: force: true`) so stderr
// is expected to carry `flags-miss.cue:8:\d+` when rendered.
const missingKeyRule = `package rules

rule: {
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

rule: {
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
// in-process run() invocations. runCLI shares the quae binary's process,
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
