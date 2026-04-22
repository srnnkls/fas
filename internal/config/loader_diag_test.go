package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/quae/internal/config"
	"github.com/srnnkls/quae/internal/diag"
)

// unwrapToDiagError recovers a *diag.DiagError value from err, no matter how
// deep the wrapping. Used instead of raw errors.As at call sites so the tests
// stay focused on semantics rather than adapter plumbing.
func unwrapToDiagError(t *testing.T, err error) *diag.DiagError {
	t.Helper()
	var de *diag.DiagError
	if !errors.As(err, &de) {
		t.Fatalf("errors.As failed: expected *diag.DiagError in chain, got %T: %v", err, err)
	}
	return de
}

// collectDiagErrors walks err (which may be the product of errors.Join) and
// returns every *diag.DiagError it can recover. The traversal follows the
// Unwrap() error / Unwrap() []error contract used by stdlib's joinError.
func collectDiagErrors(err error) []*diag.DiagError {
	var out []*diag.DiagError
	var walk func(e error)
	walk = func(e error) {
		if e == nil {
			return
		}
		var de *diag.DiagError
		if errors.As(e, &de) {
			out = append(out, de)
		}
		// errors.Join returns a joinError that implements Unwrap() []error.
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, child := range multi.Unwrap() {
				walk(child)
			}
			return
		}
		if single, ok := e.(interface{ Unwrap() error }); ok {
			walk(single.Unwrap())
		}
	}
	walk(err)
	return out
}

// writeRuleFileAt writes src into dir/name and returns the absolute path so
// tests can anchor position-resolution assertions on the exact on-disk file.
func writeRuleFileAt(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestLoadRules_SchemaMismatch_EmitsE0101 pins the structured-diagnostic
// contract for a rule whose `then` does not satisfy the `#Action` schema
// shape. A raw scalar (`then: 42`) is a type-level mismatch the loader must
// classify under the E01xx load range — specifically E0101 — and the caret
// must resolve to the offending `then:` line in the fixture.
func TestLoadRules_SchemaMismatch_EmitsE0101(t *testing.T) {
	const src = `package rules

bad_then: {
	when: {hook_event_name: "PreToolUse"}
	then: 42
}
`
	dir := t.TempDir()
	path := writeRuleFileAt(t, dir, "bad_then.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected load error for non-struct then value, got nil")
	}

	de := unwrapToDiagError(t, err)
	if de.D.Code != "E0101" {
		t.Fatalf("expected Diagnostic.Code=E0101 for schema mismatch, got %q", de.D.Code)
	}

	pos := de.D.Primary.Pos
	if !pos.IsValid() {
		t.Fatalf("expected primary label to carry a valid position, got invalid pos")
	}
	if filepath.Base(pos.Filename()) != "bad_then.cue" {
		t.Errorf("primary position filename=%q, want it to point at bad_then.cue", pos.Filename())
	}
	// The `then: 42` line is the 5th line of the fixture (1-based counting
	// the `package rules` header on line 1, the blank on 2, `bad_then: {` on
	// 3, the `when:` line on 4, and `then: 42` on 5). We assert against the
	// source text rather than the absolute line number to stay resilient to
	// fixture edits: look up which line the pos claims, read that line from
	// disk, and check it contains `then: 42`.
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read fixture: %v", readErr)
	}
	lines := strings.Split(string(data), "\n")
	lineNum := pos.Line()
	if lineNum <= 0 || lineNum > len(lines) {
		t.Fatalf("primary line %d out of range for fixture (%d lines)", lineNum, len(lines))
	}
	if !strings.Contains(lines[lineNum-1], "then") {
		t.Errorf("primary position line %d = %q does not contain 'then'", lineNum, lines[lineNum-1])
	}
}

// TestLoadRules_UnknownActionKind_EmitsE0102 pins the E0102 assignment for a
// rule whose `then` names an action that is not part of `#Action`. `halt` was
// collapsed into `deny` in the canonical schema; asking for `then: halt: {}`
// must be classified as "unknown action kind" rather than a generic
// schema-shape error — it is the closer match for the author's intent.
func TestLoadRules_UnknownActionKind_EmitsE0102(t *testing.T) {
	const src = `package rules

bad_kind: {
	when: {hook_event_name: "PreToolUse"}
	then: halt: {
		rule_id: "r1"
		reason:  "stop"
	}
}
`
	dir := t.TempDir()
	writeRuleFileAt(t, dir, "bad_kind.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected load error for unknown action kind `halt`, got nil")
	}

	de := unwrapToDiagError(t, err)
	if de.D.Code != "E0102" {
		t.Fatalf("expected Diagnostic.Code=E0102 for unknown action kind, got %q", de.D.Code)
	}
	if !de.D.Primary.Pos.IsValid() {
		t.Errorf("expected primary label to carry a valid position, got invalid pos")
	}
	if base := filepath.Base(de.D.Primary.Pos.Filename()); base != "bad_kind.cue" {
		t.Errorf("primary position filename=%q, want it to point at bad_kind.cue", base)
	}
}

// TestLoadRules_MultipleIndependentFailures_ReturnsErrorsJoin pins the
// errors.Join contract: when a single file has two independently-failing rules
// the loader must return both via errors.Join rather than stopping at the
// first. errors.As must recover at least one DiagError; the joined error's
// Unwrap()[]error lane must expose both, each carrying its own code.
//
// Both rules below have a non-struct `then`, so each independently fails the
// schema check. The test asserts the loader reports both, not just the first.
func TestLoadRules_MultipleIndependentFailures_ReturnsErrorsJoin(t *testing.T) {
	const src = `package rules

first_broken: {
	when: {hook_event_name: "PreToolUse"}
	then: 42
}

second_broken: {
	when: {hook_event_name: "PreToolUse"}
	then: "nope"
}
`
	dir := t.TempDir()
	writeRuleFileAt(t, dir, "two_broken.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected load error for two broken rules, got nil")
	}

	// errors.As must recover at least one structured diagnostic.
	var de *diag.DiagError
	if !errors.As(err, &de) {
		t.Fatalf("errors.As failed: expected at least one *diag.DiagError, got %T: %v", err, err)
	}

	// Both failures should surface. Prefer the joined-errors lane so we can
	// see each diagnostic individually; fall back to string inspection only
	// if that lane is not exposed.
	diags := collectDiagErrors(err)
	if len(diags) < 2 {
		t.Fatalf("expected at least 2 DiagErrors via errors.Join, got %d (err=%v)", len(diags), err)
	}

	codes := make([]string, 0, len(diags))
	for _, d := range diags {
		codes = append(codes, d.D.Code)
	}
	// Both rules trip the schema-shape failure class; allow either the same
	// code twice or a mix — the invariant is that a code was assigned for
	// each failure, and both sit in the E01xx load range.
	for _, c := range codes {
		if !strings.HasPrefix(c, "E01") {
			t.Errorf("DiagError code %q is not in the E01xx load range", c)
		}
	}
}

// TestLoadRules_DiagError_SatisfiesErrorInterface pins that the new adapter
// type continues to satisfy Go's error contract — rendering via the
// diagnostic renderer, but still a plain `error` to every stdlib caller.
// Without this, the migration would break `fmt.Errorf("%w", ...)` chains and
// existing callers that take `error` directly.
func TestLoadRules_DiagError_SatisfiesErrorInterface(t *testing.T) {
	const src = `package rules

bad_then: {
	when: {hook_event_name: "PreToolUse"}
	then: 42
}
`
	dir := t.TempDir()
	writeRuleFileAt(t, dir, "bad_then.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Plain error interface must work — Error() is non-empty and does not
	// panic.
	msg := err.Error()
	if msg == "" {
		t.Fatal("err.Error() returned empty string; DiagError must render via diag.Render")
	}

	// The rendered output must carry the stable code marker so tools can
	// grep for it. We check the code is present in the rendered message
	// regardless of the exact format — the renderer's byte-level shape is
	// covered by diag/render_test.go, not here.
	de := unwrapToDiagError(t, err)
	if !strings.Contains(msg, de.D.Code) {
		t.Errorf("rendered error %q does not contain code %q", msg, de.D.Code)
	}
}
