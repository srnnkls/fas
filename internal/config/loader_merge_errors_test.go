package config_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
)

// recoverMergeDiags returns every *diag.DiagError reachable from err,
// descending both the Unwrap() []error and Unwrap() error lanes.
func recoverMergeDiags(err error) []*diag.DiagError {
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

// diagMentionsFile reports whether de attributes itself to base via its
// primary position filename or its file-anchored Title.
func diagMentionsFile(de *diag.DiagError, base string) bool {
	if pos := de.D.Primary.Pos; pos.IsValid() {
		if filepath.Base(pos.Filename()) == base {
			return true
		}
	}
	return strings.Contains(de.D.Title, base)
}

// CRP-003 invariant: two distinct broken rules in two merged files surface as
// >=2 recoverable DiagErrors, each naming its OWN file (no collapse/misattribution).
func TestLoadRules_CRP003_MultiFaultAcrossFiles_SurfacesPerFileDiagErrors(t *testing.T) {
	const aSrc = `package rules

a_broken: {
	when: {hook_event_name: "PreToolUse"}
	then: halt: {
		rule_id: "a"
		reason:  "stop"
	}
}
`
	const bSrc = `package rules

import "github.com/srnnkls/fas/cue/hook"

b_broken: {
	when: hook.#PreToolUze
	then: deny: {
		rule_id: "b"
		reason:  "nope"
	}
}
`
	dir := t.TempDir()
	// a.cue: unknown action kind (`halt` is not a #Action member).
	writeRuleFileNamed(t, dir, "a.cue", aSrc)
	// b.cue: typo'd stdlib member (#PreToolUze), kept on the offending leaf.
	writeRuleFileNamed(t, dir, "b.cue", bSrc)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected load error for two distinct broken rules across files, got nil")
	}

	diags := recoverMergeDiags(err)
	if len(diags) < 2 {
		t.Fatalf("expected >=2 recoverable *diag.DiagError from the merged package, got %d (err=%v)",
			len(diags), err)
	}

	var sawA, sawB bool
	for _, de := range diags {
		if diagMentionsFile(de, "a.cue") {
			sawA = true
		}
		if diagMentionsFile(de, "b.cue") {
			sawB = true
		}
	}
	if !sawA {
		t.Errorf("no recovered diagnostic attributed to a.cue; merge collapsed or mis-attributed file origin (diags=%v)", err)
	}
	if !sawB {
		t.Errorf("no recovered diagnostic attributed to b.cue; merge collapsed or mis-attributed file origin (diags=%v)", err)
	}
}

// CRP-003 invariant: a broken rule's diagnostic Title names BOTH its file and field.
func TestLoadRules_CRP003_ErrorModel_NamesFileAndField(t *testing.T) {
	const src = `package rules

lonely_broken: {
	when: {hook_event_name: "PreToolUse"}
	then: 42
}
`
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "solo.cue", src)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected load error for non-struct then value, got nil")
	}

	diags := recoverMergeDiags(err)
	if len(diags) == 0 {
		t.Fatalf("expected at least one *diag.DiagError, got none (err=%v)", err)
	}

	var found bool
	for _, de := range diags {
		if strings.Contains(de.D.Title, "solo.cue") && strings.Contains(de.D.Title, "lonely_broken") {
			found = true
			break
		}
	}
	if !found {
		titles := make([]string, 0, len(diags))
		for _, de := range diags {
			titles = append(titles, de.D.Title)
		}
		t.Errorf("expected a diagnostic Title naming both file (solo.cue) and field (lonely_broken); got titles %q", titles)
	}
}

// CRP-003 invariant: cross-file extension of a shared OPEN helper loads without
// tripping closedness; the merged `when` carries fields from both files.
func TestLoadRules_CRP003_CrossFileOpenStructExtension_Succeeds(t *testing.T) {
	const aSrc = `package rules

_shared: {hook_event_name: "PreToolUse"}
`
	const bSrc = `package rules

_shared: {tool_name: "Bash"}

uses_shared: {
	when: _shared
	then: deny: {
		rule_id: "shared"
		reason:  "blocked"
	}
}
`
	dir := t.TempDir()
	writeRuleFileNamed(t, dir, "a.cue", aSrc)
	writeRuleFileNamed(t, dir, "b.cue", bSrc)

	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("cross-file open-struct extension of a shared helper must load; merge wrongly tripped closedness: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected exactly 1 rule (uses_shared), got %d", len(rules))
	}
	if rules[0].Then == nil || rules[0].Then.Kind != config.ActionDeny {
		t.Fatalf("expected uses_shared to decode a deny action, got %+v", rules[0].Then)
	}
	if got := rules[0].WhenMap["hook_event_name"]; got != "PreToolUse" {
		t.Errorf("when should inherit hook_event_name from a.cue's _shared; got %v", got)
	}
	if got := rules[0].WhenMap["tool_name"]; got != "Bash" {
		t.Errorf("when should inherit tool_name from b.cue's _shared extension; got %v", got)
	}
}
