package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// CRP-006 reworks the structural lint from per-file to package level (AD-6) and
// refines the bound-reference predicate (AD-5): a `when` reference whose root
// resolves to a sibling `_helper` or `#def` declared in ANY file of the package
// is allowed, while E0502/E0503 now fire across files when a selector root
// resolves to another top-level RULE's when/then/meta. CRP-001-D1 folds in:
// quoted-label rules must participate in lint.
//
// These tests assert the PACKAGE-level semantics. Under today's per-file lint
// they fail because cross-file siblings are invisible to one file's scope.

// TestLoadRules_CrossFile_HelperRefInWhen_Allowed pins AD-5: a `when` that
// references a top-level `_shared` helper declared in a SIBLING file must load
// cleanly. RED today: b.cue's per-file lint sees `_shared` as unbound -> E0501.
func TestLoadRules_CrossFile_HelperRefInWhen_Allowed(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

_shared: {tool_name: "Bash"}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

r_b: {
	when: _shared
	then: deny: {
		rule_id: "b"
		reason:  "x"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err != nil {
		for _, de := range collectDiags(err) {
			if strings.HasPrefix(de.D.Code, "E05") {
				t.Fatalf("cross-file _shared helper ref in when must be allowed; got lint %s: %v",
					de.D.Code, err)
			}
		}
		t.Fatalf("LoadRules should accept cross-file helper ref; got: %v", err)
	}
}

// TestLoadRules_CrossFile_DefRefInWhen_Allowed pins AD-5 for `#def`: a `when`
// that references a top-level `#Base` definition declared in a SIBLING file must
// load cleanly. RED today: b.cue's per-file lint sees `#Base` as unbound.
func TestLoadRules_CrossFile_DefRefInWhen_Allowed(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

#Base: {tool_name: "Bash"}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

r_b: {
	when: #Base
	then: deny: {
		rule_id: "b"
		reason:  "x"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err != nil {
		for _, de := range collectDiags(err) {
			if strings.HasPrefix(de.D.Code, "E05") {
				t.Fatalf("cross-file #Base def ref in when must be allowed; got lint %s: %v",
					de.D.Code, err)
			}
		}
		t.Fatalf("LoadRules should accept cross-file def ref; got: %v", err)
	}
}

// TestLoadRules_CrossFile_RefIntoOtherRuleThen_EmitsE0502 pins AD-6: b.cue's
// `when` reaches into rule `ra`'s `then` subtree, where `ra` is declared in a
// SIBLING file (a.cue). The package-level lint must classify this as a cross-rule
// reference -> E0502.
//
// RED today AND wrong-code-today: `ra` is unknown in b.cue's per-file rule-name
// set, so today the selector root is treated as unbound (E0501) or slips through
// — NOT E0502. The diagnostic must also be attributed to b.cue (where the
// offending `when` lives).
func TestLoadRules_CrossFile_RefIntoOtherRuleThen_EmitsE0502(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

ra: {
	when: {tool_name: "Bash"}
	then: deny: {
		rule_id: "a"
		reason:  "x"
	}
}
`)
	bPath := writeRuleFileNamed(t, dir, "b.cue", `package rules

rb: {
	when: {x: ra.then.deny.rule_id}
	then: deny: {
		rule_id: "b"
		reason:  "x"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected cross-file ref into another rule's then to be rejected, got nil error")
	}

	var e0502 bool
	for _, de := range collectDiags(err) {
		if de.D.Code == "E0502" {
			e0502 = true
			if got := tokenAtPos(t, de.D.Primary.Pos, len("ra")); got != "ra" {
				t.Errorf("E0502 primary should anchor at selector root `ra`; got %q", got)
			}
			if base := filepath.Base(de.D.Primary.Pos.Filename()); base != "b.cue" {
				t.Errorf("E0502 should be attributed to b.cue (where the when lives); got %q", base)
			}
		}
	}
	if !e0502 {
		diags := collectDiags(err)
		codes := make([]string, 0, len(diags))
		for _, de := range diags {
			codes = append(codes, de.D.Code)
		}
		t.Fatalf("expected E0502 for cross-file ref into another rule's then; got codes %v (err=%v)",
			codes, err)
	}
	_ = bPath
}

// TestLoadRules_QuotedLabelRule_ParticipatesInLint_EmitsE0502 folds in
// CRP-001-D1: a quoted-label rule (`"dash-rule": {...}`) must participate in the
// lint. Here its `when` reaches into another rule's `then` -> E0502.
func TestLoadRules_QuotedLabelRule_ParticipatesInLint_EmitsE0502(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "rules.cue", `package rules

other_rule: {
	when: {tool_name: "Bash"}
	then: deny: {
		rule_id: "other"
		reason:  "x"
	}
}

"dash-rule": {
	when: {x: other_rule.then.deny.rule_id}
	then: deny: {
		rule_id: "dash"
		reason:  "x"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected quoted-label rule's cross-rule ref to be rejected, got nil error")
	}

	var e0502 bool
	for _, de := range collectDiags(err) {
		if de.D.Code == "E0502" {
			e0502 = true
			if got := tokenAtPos(t, de.D.Primary.Pos, len("other_rule")); got != "other_rule" {
				t.Errorf("E0502 primary should anchor at selector root `other_rule`; got %q", got)
			}
		}
	}
	if !e0502 {
		diags := collectDiags(err)
		codes := make([]string, 0, len(diags))
		for _, de := range diags {
			codes = append(codes, de.D.Code)
		}
		t.Fatalf("expected E0502 from quoted-label rule participating in lint; got codes %v (err=%v)",
			codes, err)
	}
}

// TestLoadRules_CrossFile_GenuineUnboundTypo_StillEmitsE0501 is the guard: an
// identifier declared in NO file of the package must still fire E0501 after the
// predicate refinement — the cross-file allowance must not swallow real typos.
func TestLoadRules_CrossFile_GenuineUnboundTypo_StillEmitsE0501(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

_present: {tool_name: "Bash"}
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

r_b: {
	when: {x: _nonexistent}
	then: deny: {
		rule_id: "b"
		reason:  "x"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected genuine unbound ident to be rejected, got nil error")
	}

	var e0501 bool
	for _, de := range collectDiags(err) {
		if de.D.Code == "E0501" {
			e0501 = true
			if got := tokenAtPos(t, de.D.Primary.Pos, len("_nonexistent")); got != "_nonexistent" {
				t.Errorf("E0501 primary should anchor at the unbound ident `_nonexistent`; got %q", got)
			}
		}
	}
	if !e0501 {
		diags := collectDiags(err)
		codes := make([]string, 0, len(diags))
		for _, de := range diags {
			codes = append(codes, de.D.Code)
		}
		t.Fatalf("expected E0501 for genuine cross-package unbound typo; got codes %v (err=%v)",
			codes, err)
	}
}

// TestLoadRules_SelfRefIntoOwnThen_StillEmitsE0503 is the guard for existing
// behavior under the package-level rework: a same-file rule whose `when`
// references its own `.then` must still surface E0503.
func TestLoadRules_SelfRefIntoOwnThen_StillEmitsE0503(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "self.cue", `package rules

self_rule: {
	when: {tool_name: "Bash", marker: self_rule.then.deny.rule_id}
	then: deny: {
		rule_id: "self"
		reason:  "x"
	}
}
`)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected self-ref into own then to be rejected, got nil error")
	}

	var e0503 bool
	for _, de := range collectDiags(err) {
		if de.D.Code == "E0503" {
			e0503 = true
			if got := tokenAtPos(t, de.D.Primary.Pos, len("self_rule")); got != "self_rule" {
				t.Errorf("E0503 primary should anchor at the self-ref root `self_rule`; got %q", got)
			}
		}
	}
	if !e0503 {
		diags := collectDiags(err)
		codes := make([]string, 0, len(diags))
		for _, de := range diags {
			codes = append(codes, de.D.Code)
		}
		t.Fatalf("expected E0503 for self-ref into own then; got codes %v (err=%v)", codes, err)
	}
}

// TestLoadRules_CrossFile_HelperRefInThenInject_Allowed is the headline guard:
// the lint only walks `when`, so a `_shared` reference inside `then.inject.text`
// from a sibling file must load without E0501 regardless of the predicate
// rework. Documents that the lint's reach is `when`-only.
func TestLoadRules_CrossFile_HelperRefInThenInject_Allowed(t *testing.T) {
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", `package rules

_shared: "hint text"
`)
	writeRuleFileNamed(t, dir, "b.cue", `package rules

r_b: {
	when: {tool_name: "Bash"}
	then: inject: {
		rule_id:  "b"
		text:     _shared
		channel:  "agent"
		priority: 50
	}
}
`)

	_, err := config.LoadRules(dir)
	if err != nil {
		for _, de := range collectDiags(err) {
			if de.D.Code == "E0501" {
				t.Fatalf("lint must only walk when; _shared in then.inject.text must not raise E0501: %v", err)
			}
		}
		t.Fatalf("LoadRules should accept cross-file helper ref in then.inject.text; got: %v", err)
	}
}
