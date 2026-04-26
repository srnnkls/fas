package evaluator_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
	"github.com/srnnkls/fas/internal/evaluator"
)

// writeKindFile writes a full rules file (no `rule: ` prefix injected).
func writeKindFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write rule: %v", err)
	}
	return p
}

// loadOneRule loads the single rule from dir.
func loadOneRule(t *testing.T, dir string) config.Rule {
	t.Helper()
	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule loaded, got %d", len(rules))
	}
	return rules[0]
}

// compileValKind compiles a CUE value string for tests.
func compileValKind(t *testing.T, src string) cue.Value {
	t.Helper()
	ctx := cuecontext.New()
	v := ctx.CompileString(src, cue.Filename("input.cue"))
	if err := v.Err(); err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return v
}

// findDiag returns the first diagnostic carrying code, or fails.
func findDiag(t *testing.T, diags []diag.Diagnostic, code string) diag.Diagnostic {
	t.Helper()
	for _, d := range diags {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("no diagnostic with code %s found; got %d diagnostics: %+v", code, len(diags), diags)
	return diag.Diagnostic{}
}

// countDiagsWithCode returns the number of diagnostics whose Code matches.
func countDiagsWithCode(diags []diag.Diagnostic, code string) int {
	n := 0
	for _, d := range diags {
		if d.Code == code {
			n++
		}
	}
	return n
}

// kindAliasesPreamble: hidden-sibling aliases bypass the bare-kind lint.
const kindAliasesPreamble = `package rules

_int:    int
_string: string
_bool:   bool
_number: number

`

// Disjoint kinds (int vs string) emit E0303 with one KindMismatch Reason.
func TestLocalize_KindMismatch_IntVsString_EmitsE0303WithReason(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "kind_int_str.cue", kindAliasesPreamble+`rule: {
	when: {count: _int}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: "5"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0303")

	if d.Severity != diag.SeverityError {
		t.Errorf("Severity = %v, want SeverityError", d.Severity)
	}
	if d.Title != "type mismatch" {
		t.Errorf("Title = %q, want %q", d.Title, "type mismatch")
	}
	if !d.Primary.Pos.IsValid() {
		t.Fatalf("Primary.Pos must be valid; got %v", d.Primary.Pos)
	}
	if d.Primary.Len <= 0 {
		t.Errorf("Primary.Len = %d, want >0 (caret span of the constraint)", d.Primary.Len)
	}

	if got := len(d.Primary.Reasons); got != 1 {
		t.Fatalf("Primary.Reasons length = %d, want 1 (singular KindMismatch); reasons=%+v", got, d.Primary.Reasons)
	}
	want := diag.KindMismatch{
		Want:   cue.IntKind,
		Got:    cue.StringKind,
		Actual: `"5"`,
	}
	got, ok := d.Primary.Reasons[0].(diag.KindMismatch)
	if !ok {
		t.Fatalf("Primary.Reasons[0] type = %T, want diag.KindMismatch", d.Primary.Reasons[0])
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Primary.Reasons[0] = %+v, want %+v", got, want)
	}
}

// bool vs int is a disjoint kind pair and emits a KindMismatch Reason.
func TestLocalize_KindMismatch_BoolVsNumber(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "kind_bool_num.cue", kindAliasesPreamble+`rule: {
	when: {enabled: _bool}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	// `1` compiles to cue.IntKind; cue.BoolKind is disjoint from IntKind.
	input := compileValKind(t, `{enabled: 1}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0303")

	if len(d.Primary.Reasons) != 1 {
		t.Fatalf("Primary.Reasons length = %d, want 1; reasons=%+v", len(d.Primary.Reasons), d.Primary.Reasons)
	}
	km, ok := d.Primary.Reasons[0].(diag.KindMismatch)
	if !ok {
		t.Fatalf("Primary.Reasons[0] type = %T, want diag.KindMismatch", d.Primary.Reasons[0])
	}
	if km.Want != cue.BoolKind {
		t.Errorf("KindMismatch.Want = %v, want cue.BoolKind", km.Want)
	}
	if km.Got != cue.IntKind {
		t.Errorf("KindMismatch.Got = %v, want cue.IntKind", km.Got)
	}
	if km.Actual != "1" {
		t.Errorf("KindMismatch.Actual = %q, want %q", km.Actual, "1")
	}
}

// Same-kind matching input produces zero diagnostics.
func TestLocalize_KindMatch_IntVsInt_NoKindMismatch(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "kind_int_int.cue", kindAliasesPreamble+`rule: {
	when: {count: _int}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: 5}`)

	matches, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("int vs int must match the rule; got %d matches", len(matches))
	}
	for _, d := range diags {
		for _, r := range d.Primary.Reasons {
			if _, ok := r.(diag.KindMismatch); ok {
				t.Errorf("kind-compatible input produced a KindMismatch reason: %+v", d)
			}
		}
	}
}

// Bounded-int refinement failure does not produce a KindMismatch Reason.
func TestLocalize_KindMatch_BoundedIntFails_NoKindMismatch(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "kind_bound.cue", kindAliasesPreamble+`rule: {
	when: {count: _int & >=10}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: 5}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	for _, d := range diags {
		for _, r := range d.Primary.Reasons {
			if _, ok := r.(diag.KindMismatch); ok {
				t.Errorf("bounded-int (same-kind) failure produced a KindMismatch reason: %+v", d)
			}
		}
	}
}

// number/int lattice overlap never surfaces as a KindMismatch.
func TestLocalize_KindLatticeOverlap_NumberVsInt_NoKindMismatch(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	t.Run("rule_number_input_int_matches", func(t *testing.T) {
		dir := t.TempDir()
		writeKindFile(t, dir, "num_int.cue", kindAliasesPreamble+`rule: {
	when: {x: _number}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
		rule := loadOneRule(t, dir)
		input := compileValKind(t, `{x: 5}`)

		matches, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(matches) != 1 {
			t.Errorf("number vs int should match; got %d matches", len(matches))
		}
		for _, d := range diags {
			for _, r := range d.Primary.Reasons {
				if _, ok := r.(diag.KindMismatch); ok {
					t.Errorf("number-vs-int produced a KindMismatch reason: %+v", d)
				}
			}
		}
	})

	t.Run("rule_int_input_float_no_kindmismatch", func(t *testing.T) {
		dir := t.TempDir()
		writeKindFile(t, dir, "int_float.cue", kindAliasesPreamble+`rule: {
	when: {x: _int}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
		rule := loadOneRule(t, dir)
		input := compileValKind(t, `{x: 5.5}`)

		_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		// Whatever the evaluator chooses to emit, it must not be a
		// KindMismatch — number and int share lattice space.
		for _, d := range diags {
			for _, r := range d.Primary.Reasons {
				if _, ok := r.(diag.KindMismatch); ok {
					t.Errorf("int-vs-float produced a KindMismatch reason: %+v", d)
				}
			}
		}
	})
}

// Union kind (int | string) vs bool is disjoint and emits KindMismatch.
func TestLocalize_CompoundKind_IntOrString_vs_Bool(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "compound_bool.cue", kindAliasesPreamble+`rule: {
	when: {x: _int | _string}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{x: true}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0303")

	if len(d.Primary.Reasons) != 1 {
		t.Fatalf("Primary.Reasons length = %d, want 1; reasons=%+v", len(d.Primary.Reasons), d.Primary.Reasons)
	}
	km, ok := d.Primary.Reasons[0].(diag.KindMismatch)
	if !ok {
		t.Fatalf("Primary.Reasons[0] type = %T, want diag.KindMismatch", d.Primary.Reasons[0])
	}
	// Want is the rule's IncompleteKind — a union of IntKind|StringKind.
	// Assert containment rather than exact equality so the implementer
	// may normalise the union however they choose, as long as the kinds
	// are faithfully preserved.
	if km.Want&cue.IntKind == 0 {
		t.Errorf("KindMismatch.Want = %v, must include cue.IntKind (%v)", km.Want, cue.IntKind)
	}
	if km.Want&cue.StringKind == 0 {
		t.Errorf("KindMismatch.Want = %v, must include cue.StringKind (%v)", km.Want, cue.StringKind)
	}
	if km.Got != cue.BoolKind {
		t.Errorf("KindMismatch.Got = %v, want cue.BoolKind", km.Got)
	}
	if km.Actual != "true" {
		t.Errorf("KindMismatch.Actual = %q, want %q", km.Actual, "true")
	}
}

// Union kind (int | string) vs int overlaps and emits no KindMismatch.
func TestLocalize_CompoundKind_IntOrString_vs_Int(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "compound_int.cue", kindAliasesPreamble+`rule: {
	when: {x: _int | _string}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{x: 7}`)

	matches, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(matches) != 1 {
		t.Errorf("int | string vs int should match; got %d matches", len(matches))
	}
	for _, d := range diags {
		for _, r := range d.Primary.Reasons {
			if _, ok := r.(diag.KindMismatch); ok {
				t.Errorf("int-in-union produced a spurious KindMismatch: %+v", d)
			}
		}
	}
}

// Kind-mismatch diagnostic shape: code, severity, title, valid span.
func TestLocalize_KindMismatch_DiagnosticShape(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	rulePath := writeKindFile(t, dir, "shape.cue", kindAliasesPreamble+`rule: {
	when: {count: _int}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: "abc"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0303")

	if d.Code != "E0303" {
		t.Errorf("Code = %q, want %q", d.Code, "E0303")
	}
	if d.Severity != diag.SeverityError {
		t.Errorf("Severity = %v, want SeverityError", d.Severity)
	}
	if d.Title != "type mismatch" {
		t.Errorf("Title = %q, want %q", d.Title, "type mismatch")
	}
	if !d.Primary.Pos.IsValid() {
		t.Fatalf("Primary.Pos must be valid; got %v", d.Primary.Pos)
	}
	// Filename routed through CUE's loader → contains the fixture stem.
	stem := strings.TrimSuffix(filepath.Base(rulePath), filepath.Ext(rulePath))
	if fn := d.Primary.Pos.Filename(); !strings.Contains(fn, stem) {
		t.Errorf("Primary.Pos.Filename = %q, want to contain fixture stem %q", fn, stem)
	}
	if d.Primary.Len <= 0 {
		t.Errorf("Primary.Len = %d, want >0", d.Primary.Len)
	}
}

// Kind-mismatch leaf yields exactly one diagnostic (no E0301 fallback).
func TestLocalize_KindMismatch_ShortCircuitsLeaf(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "short.cue", kindAliasesPreamble+`rule: {
	when: {count: _int}
	then: deny: {rule_id: "r", reason: "nope"}
}
`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{count: "5"}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := len(diags); got != 1 {
		t.Fatalf("kind-mismatch leaf must yield exactly one diagnostic; got %d: %+v", got, diags)
	}
	if n := countDiagsWithCode(diags, "E0303"); n != 1 {
		t.Errorf("want 1 E0303, got %d (diags=%+v)", n, diags)
	}
	if n := countDiagsWithCode(diags, "E0301"); n != 0 {
		t.Errorf("kind-mismatch must not also emit an E0301 fallback; got %d E0301 (diags=%+v)", n, diags)
	}
}
