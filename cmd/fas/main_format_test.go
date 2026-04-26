package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Fixtures for format/color tests
// -----------------------------------------------------------------------------

// formatTestRule is a non-firing rule: stdin `claudeBashInput("ls")` does not
// expose `tool_input.flags.force`, so localize yields an E0201 absent-key
// diagnostic. Reused across every --format / --color assertion in this file.
const formatTestRule = `package rules

rule: {
	when: {
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: {
			flags: force: true
		}
	}
	then: deny: {
		rule_id:  "fmt-rule"
		reason:   "force flag"
		severity: "HIGH"
	}
}
`

// runFormatCLI is a thin wrapper around runCLI that pre-clears FAS_* env vars
// so inherited state from the ambient shell never leaks into a test. Each
// call re-establishes a clean env; individual tests set specific vars via
// t.Setenv before invoking.
func runFormatCLI(t *testing.T, stdin []byte, args ...string) runResult {
	t.Helper()
	t.Setenv("FAS_FORMAT", "")
	t.Setenv("FAS_COLOR", "")
	t.Setenv("NO_COLOR", "")
	return runCLI(t, stdin, args...)
}

// runFormatCLIRaw is the runCLI variant that preserves whatever env the
// caller has already configured via t.Setenv. Used by tests that must
// explicitly set FAS_* or NO_COLOR before invoking.
func runFormatCLIRaw(t *testing.T, stdin []byte, args ...string) runResult {
	t.Helper()
	return runCLI(t, stdin, args...)
}

// -----------------------------------------------------------------------------
// --format=json on `fas eval`
// -----------------------------------------------------------------------------

// TestRun_FormatJSON_EmitsNDJSON: each line on stderr parses as a JSON
// object whose top-level keys match the diag.RenderJSON schema.
func TestRun_FormatJSON_EmitsNDJSON(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
		"--format=json",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	if len(res.stderr) == 0 {
		t.Fatalf("--format=json: expected diagnostics on stderr, got empty")
	}
	// Each non-empty line must parse as its own JSON object.
	lines := strings.Split(strings.TrimRight(string(res.stderr), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatalf("--format=json: no diagnostic lines on stderr")
	}
	for i, line := range lines {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d not valid JSON: %v\nline=%q", i, err, line)
		}
		if _, ok := obj["code"]; !ok {
			t.Errorf("line %d missing `code` field: %q", i, line)
		}
	}
}

// TestRun_FormatSARIF_SingleDocument: --format=sarif emits a single SARIF
// 2.1.0 JSON document containing version and runs keys.
func TestRun_FormatSARIF_SingleDocument(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
		"--format=sarif",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	var doc struct {
		Version string `json:"version"`
		Runs    []any  `json:"runs"`
	}
	if err := json.Unmarshal(res.stderr, &doc); err != nil {
		t.Fatalf("--format=sarif: stderr is not valid JSON: %v\nstderr=%s",
			err, res.stderr)
	}
	if doc.Version != "2.1.0" {
		t.Errorf("SARIF version=%q want \"2.1.0\"", doc.Version)
	}
	if len(doc.Runs) == 0 {
		t.Errorf("SARIF runs is empty; expected at least one run")
	}
}

// TestRun_FormatText_Default: no --format flag → existing text behavior.
// Asserts: no JSON object on stderr (text has an `error[EXXXX]` header
// prefix that never appears in a JSON line), and stderr is non-empty.
func TestRun_FormatText_Default(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	s := string(res.stderr)
	if !strings.Contains(s, "E0201") {
		t.Errorf("default text format should carry the E0201 header; stderr=%q", s)
	}
	// Reject accidental JSON: a text-format stderr must not start with `{`.
	if strings.HasPrefix(strings.TrimSpace(s), "{") {
		t.Errorf("default --format=text leaked JSON; stderr=%q", s)
	}
}

// TestRun_FasFormatEnv_JSON: FAS_FORMAT=json without the flag produces
// the same JSON shape as --format=json.
func TestRun_FasFormatEnv_JSON(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	t.Setenv("FAS_FORMAT", "json")
	t.Setenv("FAS_COLOR", "")
	t.Setenv("NO_COLOR", "")

	res := runFormatCLIRaw(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	lines := strings.Split(strings.TrimRight(string(res.stderr), "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("FAS_FORMAT=json: line %d not JSON: %v\nline=%q", i, err, line)
		}
	}
}

// TestRun_FlagWinsOverEnv_Format: --format=text with FAS_FORMAT=json →
// flag wins (text output, not JSON).
func TestRun_FlagWinsOverEnv_Format(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	t.Setenv("FAS_FORMAT", "json")
	t.Setenv("FAS_COLOR", "")
	t.Setenv("NO_COLOR", "")

	res := runFormatCLIRaw(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
		"--format=text",
	)

	if res.exit != 0 {
		t.Fatalf("exit=%d want 0; stderr=%s", res.exit, res.stderr)
	}
	s := string(res.stderr)
	if strings.HasPrefix(strings.TrimSpace(s), "{") {
		t.Errorf("--format=text should win over FAS_FORMAT=json; stderr=%q", s)
	}
	if !strings.Contains(s, "E0201") {
		t.Errorf("expected text-format E0201 header; stderr=%q", s)
	}
}

// -----------------------------------------------------------------------------
// --color on text format
// -----------------------------------------------------------------------------

// TestRun_ColorNever_NoANSI: --color=never strips ANSI escapes from
// text-format output.
func TestRun_ColorNever_NoANSI(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
		"--color=never",
	)

	if strings.Contains(string(res.stderr), "\x1b[") {
		t.Errorf("--color=never must not produce ANSI escapes; stderr=%q", res.stderr)
	}
}

// TestRun_ColorAlways_ANSIEscapes: --color=always emits ANSI even though
// the test environment is not a TTY.
func TestRun_ColorAlways_ANSIEscapes(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
		"--color=always",
	)

	if !strings.Contains(string(res.stderr), "\x1b[") {
		t.Errorf("--color=always should emit ANSI escapes; stderr=%q", res.stderr)
	}
}

// TestRun_ColorAuto_NonTTY_NoANSI: --color=auto in a non-TTY test env →
// no ANSI escapes.
func TestRun_ColorAuto_NonTTY_NoANSI(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
		"--color=auto",
	)

	if strings.Contains(string(res.stderr), "\x1b[") {
		t.Errorf("--color=auto in non-TTY env must not emit ANSI; stderr=%q",
			res.stderr)
	}
}

// TestRun_NoColorEnv_SuppressesANSI: NO_COLOR=1 with no --color flag →
// ANSI escapes suppressed.
func TestRun_NoColorEnv_SuppressesANSI(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	t.Setenv("NO_COLOR", "1")
	t.Setenv("FAS_COLOR", "")
	t.Setenv("FAS_FORMAT", "")

	res := runFormatCLIRaw(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
	)

	if strings.Contains(string(res.stderr), "\x1b[") {
		t.Errorf("NO_COLOR=1 must suppress ANSI; stderr=%q", res.stderr)
	}
}

// TestRun_FasColorWinsOverNoColor: FAS_COLOR=always with NO_COLOR=1 →
// fas-specific env wins, ANSI present.
func TestRun_FasColorWinsOverNoColor(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	t.Setenv("FAS_COLOR", "always")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FAS_FORMAT", "")

	res := runFormatCLIRaw(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
	)

	if !strings.Contains(string(res.stderr), "\x1b[") {
		t.Errorf("FAS_COLOR=always must override NO_COLOR=1; stderr=%q",
			res.stderr)
	}
}

// TestRun_ColorFlagWinsOverNoColor: --color=always + NO_COLOR=1 →
// flag wins, ANSI present.
func TestRun_ColorFlagWinsOverNoColor(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	t.Setenv("NO_COLOR", "1")
	t.Setenv("FAS_COLOR", "")
	t.Setenv("FAS_FORMAT", "")

	res := runFormatCLIRaw(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
		"--color=always",
	)

	if !strings.Contains(string(res.stderr), "\x1b[") {
		t.Errorf("--color=always must override NO_COLOR=1; stderr=%q",
			res.stderr)
	}
}

// TestRun_SARIF_ColorAlways_NoANSI: color is text-only — SARIF must stay
// plain JSON even with --color=always.
func TestRun_SARIF_ColorAlways_NoANSI(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--explain",
		"--format=sarif",
		"--color=always",
	)

	if strings.Contains(string(res.stderr), "\x1b[") {
		t.Errorf("SARIF must not carry ANSI escapes; stderr=%q", res.stderr)
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(res.stderr, &doc); err != nil {
		t.Fatalf("SARIF+color=always must still parse as JSON: %v", err)
	}
}

// TestRun_UnknownFormat_Exit2: unknown --format value → exit 2 with a
// stderr diagnostic.
func TestRun_UnknownFormat_Exit2(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"eval",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--format=xml",
	)

	if res.exit != 2 {
		t.Errorf("unknown --format should exit 2; got %d; stderr=%s",
			res.exit, res.stderr)
	}
	if len(res.stderr) == 0 {
		t.Errorf("unknown --format should emit a stderr diagnostic; got empty")
	}
}

// -----------------------------------------------------------------------------
// `fas explain` subcommand — flags work identically
// -----------------------------------------------------------------------------

// TestRun_Explain_FormatJSON: `fas explain <rule_id> --format=json` emits
// JSON-per-diagnostic on stderr when the rule does not match.
func TestRun_Explain_FormatJSON(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"explain", "fmt-rule",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--format=json",
	)

	if res.exit != 1 {
		t.Fatalf("explain with non-match should exit 1; got %d; stderr=%s",
			res.exit, res.stderr)
	}
	lines := strings.Split(strings.TrimRight(string(res.stderr), "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("explain --format=json line %d not JSON: %v\nline=%q",
				i, err, line)
		}
	}
}

// TestRun_Explain_ColorNever: `fas explain --color=never` strips ANSI.
func TestRun_Explain_ColorNever(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"explain", "fmt-rule",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--color=never",
	)

	if strings.Contains(string(res.stderr), "\x1b[") {
		t.Errorf("explain --color=never must not carry ANSI; stderr=%q",
			res.stderr)
	}
}

// TestRun_Explain_ColorAlways: `fas explain --color=always` emits ANSI.
func TestRun_Explain_ColorAlways(t *testing.T) {
	projectDir := writeRuleFiles(t, map[string]string{
		"fmt.cue": formatTestRule,
	})
	globalDir := emptyRulesDir(t)

	res := runFormatCLI(t, claudeBashInput("ls"),
		"explain", "fmt-rule",
		"--harness", "claude",
		"--config", projectDir,
		"--global-config", globalDir,
		"--color=always",
	)

	if !strings.Contains(string(res.stderr), "\x1b[") {
		t.Errorf("explain --color=always must carry ANSI; stderr=%q",
			res.stderr)
	}
}
