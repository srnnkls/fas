package evaluator_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
	"github.com/srnnkls/fas/internal/evaluator"
)

// firstKeyMissingReason returns the single KeyMissing Reason on d.Primary.
func firstKeyMissingReason(t *testing.T, d diag.Diagnostic) diag.KeyMissing {
	t.Helper()
	var found []diag.KeyMissing
	for _, r := range d.Primary.Reasons {
		if km, ok := r.(diag.KeyMissing); ok {
			found = append(found, km)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one KeyMissing Reason on Primary; got %d (all reasons=%+v)",
			len(found), d.Primary.Reasons)
	}
	return found[0]
}

// Closest key within distance 2 wins as Suggestion.
func TestLocalize_E0201_KeyMissing_Suggestion_WithinDistance2(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "flags.cue", `{
		when: {flags: true}
		then: deny: {rule_id: "r", reason: "nope"}
	}`)
	rule := loadOne(t, dir)

	input := compileVal(t, `{flag: true, forced: false}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiagWithCode(t, diags, "E0201")

	km := firstKeyMissingReason(t, d)

	if km.Key != "flags" {
		t.Errorf("KeyMissing.Key = %q, want %q", km.Key, "flags")
	}
	wantAvailable := []string{"flag", "forced"}
	if !reflect.DeepEqual(km.AvailableKeys, wantAvailable) {
		t.Errorf("KeyMissing.AvailableKeys = %v, want %v", km.AvailableKeys, wantAvailable)
	}
	if km.Suggestion != "flag" {
		t.Errorf("KeyMissing.Suggestion = %q, want %q (distance 1 wins over distance 4)",
			km.Suggestion, "flag")
	}
}

// All candidates beyond distance 2 yield an empty Suggestion.
func TestLocalize_E0201_KeyMissing_NoSuggestionBeyondThreshold(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "command.cue", `{
		when: {command: "rm"}
		then: deny: {rule_id: "r", reason: "nope"}
	}`)
	rule := loadOne(t, dir)

	input := compileVal(t, `{file_path: "/etc/passwd"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiagWithCode(t, diags, "E0201")

	km := firstKeyMissingReason(t, d)

	if km.Key != "command" {
		t.Errorf("KeyMissing.Key = %q, want %q", km.Key, "command")
	}
	wantAvailable := []string{"file_path"}
	if !reflect.DeepEqual(km.AvailableKeys, wantAvailable) {
		t.Errorf("KeyMissing.AvailableKeys = %v, want %v", km.AvailableKeys, wantAvailable)
	}
	if km.Suggestion != "" {
		t.Errorf("KeyMissing.Suggestion = %q, want \"\" (all distances > 2)", km.Suggestion)
	}
}

// Empty parent yields non-nil empty AvailableKeys and empty Suggestion.
func TestLocalize_E0201_KeyMissing_EmptyParent_NoSuggestion(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "force.cue", `{
		when: {force: true}
		then: deny: {rule_id: "r", reason: "nope"}
	}`)
	rule := loadOne(t, dir)

	// Empty struct as input — parent of `force` has zero keys.
	input := compileVal(t, `{}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiagWithCode(t, diags, "E0201")

	km := firstKeyMissingReason(t, d)

	if km.Key != "force" {
		t.Errorf("KeyMissing.Key = %q, want %q", km.Key, "force")
	}
	if km.AvailableKeys == nil {
		t.Errorf("KeyMissing.AvailableKeys must be non-nil empty slice (F5); got nil")
	}
	if len(km.AvailableKeys) != 0 {
		t.Errorf("KeyMissing.AvailableKeys must be empty; got %v", km.AvailableKeys)
	}
	if km.Suggestion != "" {
		t.Errorf("KeyMissing.Suggestion = %q, want \"\" (no keys to suggest from)", km.Suggestion)
	}
}

// AvailableKeys is sorted alphabetically regardless of source order.
func TestLocalize_E0201_KeyMissing_AvailableKeysSortedAlphabetically(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "missing.cue", `{
		when: {missing: true}
		then: deny: {rule_id: "r", reason: "nope"}
	}`)
	rule := loadOne(t, dir)

	// Input keys written in non-alphabetical order to prove we rely on
	// listKeys sorting, not source iteration order.
	input := compileVal(t, `{zeta: 1, alpha: 2, mu: 3, beta: 4}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiagWithCode(t, diags, "E0201")

	km := firstKeyMissingReason(t, d)

	want := []string{"alpha", "beta", "mu", "zeta"}
	if !reflect.DeepEqual(km.AvailableKeys, want) {
		t.Errorf("KeyMissing.AvailableKeys = %v, want %v (sorted alphabetically)",
			km.AvailableKeys, want)
	}
}

// Legacy Help line stays populated alongside the Reasons payload.
func TestLocalize_E0201_KeyMissing_LegacyHelpIntact(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeLocalizeRule(t, dir, "legacy.cue", `{
		when: {flags: true}
		then: deny: {rule_id: "r", reason: "nope"}
	}`)
	rule := loadOne(t, dir)

	input := compileVal(t, `{flag: true, forced: false}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiagWithCode(t, diags, "E0201")

	// Structured payload is present.
	_ = firstKeyMissingReason(t, d)

	// Legacy Help line is also present and contains the actual keys — byte
	// equality with v0 format so downstream tooling (scrut goldens, simple
	// grep consumers) keeps working during the Reason migration window.
	if d.Help == "" {
		t.Fatal("E0201 Help must remain populated alongside Reasons (NF5 additive)")
	}
	if !strings.Contains(d.Help, "has keys:") {
		t.Errorf("E0201 Help should retain legacy `has keys:` phrasing; got %q", d.Help)
	}
	if !strings.Contains(d.Help, "flag") || !strings.Contains(d.Help, "forced") {
		t.Errorf("E0201 Help should list input keys (flag, forced); got %q", d.Help)
	}
}
