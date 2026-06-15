package config_test

import (
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/evaluator"
)

// PRE-CHANGE BASELINE (CRP-015) — characterization tests for the CUE rule
// loader. These pin the OBSERVABLE behavior of LoadRules + Evaluate over a
// flat rules directory BEFORE the per-file-isolation -> merged-package
// refactor. They must stay green; a diff in any pinned value is a regression
// to investigate, not a test to silently update.
//
// This file pins Rule.Source and ORDER (incl. fired-trace) over a clean load.
// Diagnostic primary Pos is the third refactor-sensitive property; it is ALREADY
// pinned by the sibling diagnostic suites and they ARE this baseline's Pos guard:
//   - internal/config/loader_diag_test.go  (Primary.Pos filename + line)
//   - internal/config/lint_diag_test.go    (Primary.Pos token-at-pos)
// A clean flat load emits no localized diagnostic, so there is nothing to pin here.
//
// Properties pinned and the production consumers that depend on each:
//
//	Rule.Source ("<path-passed-to-LoadRules>/<file>:<field>")
//	  - internal/config/loader.go:extractFileRules  (assigns rule.Source)
//	  - internal/evaluator/evaluator.go:checkWhen    (rule.Source in error text)
//	  - cmd/fas/main.go:renderExplain                ("fired: <id> (<source>)")
//	  - cmd/fas/main.go:primeFileCache               (ruleSourcePath strips :field)
//	  - cmd/fas/main.go:ruleSourcePath               (LastIndex ":" split)
//	  - cmd/fas/main.go:ruleIDForDiag                (path-vs-diag-filename match)
//
//	Rule ORDER (files alphabetical; within a file, declaration order)
//	  - internal/config/loader.go:LoadRules          (slices.Sort(names))
//	  - internal/config/loader.go:extractFileRules   (Fields iteration order)
//	  - internal/evaluator/evaluator.go:Evaluate     (matches in source order)
//	  - cmd/fas/main.go:renderExplain                (fired trace order)
//
//	Diagnostic primary Pos (source position / filename)
//	  - internal/evaluator/localize.go               (builds Diagnostic.Primary)
//	  - cmd/fas/main.go:ruleIDForDiag                (d.Primary.Pos.Filename())
//	  - internal/diag/render.go                      (renders the frame)
//
// The fixture testdata/baseline_flat is the flat-dir corpus:
//   a_bash.cue   -> deny_bash        (Bash deny)
//   m_multi.cue  -> charlie, alpha   (two rules, declared charlie-then-alpha)
//   z_write.cue  -> deny_write       (Write deny)

const baselineFlatDir = "testdata/baseline_flat"

// TestBaseline_FlatDir_RuleSourceStrings pins the EXACT Rule.Source value for
// every rule in declaration/file order. Source encodes the loader-relative
// file path plus the top-level field name, joined by ':'. Swapping any two
// entries or altering a path/field segment must fail this assertion.
func TestBaseline_FlatDir_RuleSourceStrings(t *testing.T) {
	rules, err := config.LoadRules(baselineFlatDir)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", baselineFlatDir, err)
	}

	wantSources := []string{
		"testdata/baseline_flat/a_bash.cue:deny_bash",
		"testdata/baseline_flat/m_multi.cue:charlie",
		"testdata/baseline_flat/m_multi.cue:alpha",
		"testdata/baseline_flat/z_write.cue:deny_write",
	}
	if len(rules) != len(wantSources) {
		t.Fatalf("rule count = %d, want %d", len(rules), len(wantSources))
	}
	for i, want := range wantSources {
		if rules[i].Source != want {
			t.Errorf("rules[%d].Source = %q, want %q", i, rules[i].Source, want)
		}
	}
}

// TestBaseline_FlatDir_RuleOrder pins the returned ordering: files are visited
// alphabetically (a_bash, m_multi, z_write) and, within a file, rules keep
// declaration order. m_multi.cue declares charlie BEFORE alpha, so a loader
// that ever sorted fields alphabetically would emit alpha first and fail here.
func TestBaseline_FlatDir_RuleOrder(t *testing.T) {
	rules, err := config.LoadRules(baselineFlatDir)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", baselineFlatDir, err)
	}

	wantRuleIDs := []string{"a-bash", "m-charlie", "m-alpha", "z-write"}
	if len(rules) != len(wantRuleIDs) {
		t.Fatalf("rule count = %d, want %d", len(rules), len(wantRuleIDs))
	}
	got := make([]string, len(rules))
	for i := range rules {
		if rules[i].Then == nil {
			t.Fatalf("rules[%d].Then is nil", i)
		}
		got[i] = rules[i].Then.RuleID
	}
	for i := range wantRuleIDs {
		if got[i] != wantRuleIDs[i] {
			t.Fatalf("rule order = %v, want %v", got, wantRuleIDs)
		}
	}
}

// TestBaseline_FlatDir_FiredTraceOrder pins the fired-trace order Evaluate
// produces over the flat corpus for a Bash PreToolUse input. Matches follow
// source order; the Write-only rule (z-write) must NOT fire for a Bash input.
// Each fired entry records (ruleID, Source) — the exact pair cmd/fas prints in
// its "fired: <id> (<source>)" trace.
func TestBaseline_FlatDir_FiredTraceOrder(t *testing.T) {
	rules, err := config.LoadRules(baselineFlatDir)
	if err != nil {
		t.Fatalf("LoadRules(%s): %v", baselineFlatDir, err)
	}

	ctx := cuecontext.New()
	input := ctx.CompileString(`{hook_event_name: "PreToolUse", tool_name: "Bash"}`)
	if err := input.Err(); err != nil {
		t.Fatalf("compile input: %v", err)
	}

	matches, _, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	type fired struct{ ruleID, source string }
	want := []fired{
		{"a-bash", "testdata/baseline_flat/a_bash.cue:deny_bash"},
		{"m-charlie", "testdata/baseline_flat/m_multi.cue:charlie"},
		{"m-alpha", "testdata/baseline_flat/m_multi.cue:alpha"},
	}
	if len(matches) != len(want) {
		t.Fatalf("fired count = %d, want %d (matches=%+v)", len(matches), len(want), matches)
	}
	for i, w := range want {
		id := ""
		if matches[i].Action != nil {
			id = matches[i].Action.RuleID
		}
		if id != w.ruleID || matches[i].Rule.Source != w.source {
			t.Errorf("fired[%d] = (%q, %q), want (%q, %q)",
				i, id, matches[i].Rule.Source, w.ruleID, w.source)
		}
	}
}
