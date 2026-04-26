package evaluator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
	"github.com/srnnkls/fas/internal/evaluator"
)

// -----------------------------------------------------------------------------
// T9 — Provenance walk.
//
// A diagnostic's Notes carry one `Provenance` Label per cross-file conjunct in
// `ruleNext.Expr()` (deduped by file+line, sorted, capped at 3). Conjuncts
// sharing the host file don't produce entries. Invalid positions are skipped.
// -----------------------------------------------------------------------------

// findProvenance returns the Provenance reasons harvested from d.Notes.
func findProvenance(notes []diag.Label) []diag.Provenance {
	var out []diag.Provenance
	for _, lbl := range notes {
		for _, r := range lbl.Reasons {
			if p, ok := r.(diag.Provenance); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// Test 1: a cross-file conjunct (stdlib path.#systemInCommand) contributes a
// Provenance entry when the leaf fails. The rule file is main.cue; the
// regex conjunct lives in the stdlib path.cue overlay. We unify it with a
// second regex at the leaf and feed an input matching neither.
func TestProvenance_CrossFileConjunct_ProducesEntry(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	rulePath := filepath.Join(dir, "main.cue")
	if err := os.WriteFile(rulePath, []byte(`package rules

import (
	"github.com/srnnkls/fas/cue/path"
)

myrule: {
	when: {
		tool_input: command: path.#systemInCommand & =~"^rm"
	}
	then: deny: {rule_id: "r", reason: "no"}
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	rules, err := config.LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	ctx := cuecontext.New()
	input := ctx.CompileString(`{
	tool_input: command: "ls /tmp"
}`)
	if err := input.Err(); err != nil {
		t.Fatalf("compile input: %v", err)
	}

	_, diags, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(diags) == 0 {
		t.Fatalf("expected at least one diagnostic, got 0")
	}
	// The leaf failure is on command — E0301 with cross-file conjunct(s).
	d := findDiag(t, diags, "E0301")

	prov := findProvenance(d.Notes)
	if len(prov) == 0 {
		t.Fatalf("want ≥1 Provenance Note from stdlib path.cue; got 0. Notes=%+v", d.Notes)
	}
	// At least one entry must reference a file DIFFERENT from the host file.
	found := false
	for _, p := range prov {
		if p.Span.File == "" {
			continue
		}
		if !strings.Contains(p.Span.File, "main.cue") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no Provenance entry referenced a non-host file; got spans=%+v", prov)
	}
}

// Test 2: all conjuncts in the same file as f.Value → no Provenance entries.
func TestProvenance_SameFileOnly_NoProvenanceNote(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	writeKindFile(t, dir, "same_file.cue", kindAliasesPreamble+`rule: {
		when: {x: _int & >=5}
		then: deny: {rule_id: "r", reason: "nope"}
	}
	`)
	rule := loadOneRule(t, dir)

	input := compileValKind(t, `{x: 3}`)

	_, diags, err := evaluator.Evaluate([]config.Rule{rule}, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	d := findDiag(t, diags, "E0301")
	if prov := findProvenance(d.Notes); len(prov) > 0 {
		t.Errorf("same-file leaf must not produce Provenance entries; got %+v", prov)
	}
}

// Test 3: the helper caps at 3 entries and sorts deterministically. This hits
// the helper directly because the lint rejects cross-file refs inside a rule
// file; we construct the value by compiling separate files and unifying.
// Each file contributes a conjunct that isn't subsumed by any other so CUE's
// Eval() preserves all of them.
func TestProvenance_CapAtThree_SortedByFileThenLine(t *testing.T) {
	ctx := cuecontext.New()
	parts := []struct{ file, body string }{
		{"z.cue", `{x: string & =~"^a"}`},
		{"y.cue", `{x: string & =~"b"}`},
		{"x.cue", `{x: string & =~"c"}`},
		{"w.cue", `{x: string & =~"d"}`},
		{"v.cue", `{x: string & =~"e"}`},
	}
	val := ctx.CompileString(`{x: string}`, cue.Filename("host.cue"))
	if err := val.Err(); err != nil {
		t.Fatalf("compile host: %v", err)
	}
	for _, p := range parts {
		v := ctx.CompileString(p.body, cue.Filename(p.file))
		if err := v.Err(); err != nil {
			t.Fatalf("compile %s: %v", p.file, err)
		}
		val = val.Unify(v)
	}
	x := val.LookupPath(cue.MakePath(cue.Str("x")))

	labels := evaluator.ProvenanceNotesForTest(x, "host.cue")
	if got := len(labels); got != 3 {
		t.Fatalf("cap at 3; got %d entries: %+v", got, labels)
	}
	// Must be sorted by (File, Line) — filenames sort alphabetically: v, w, x.
	wantFiles := []string{"v.cue", "w.cue", "x.cue"}
	for i, lbl := range labels {
		if len(lbl.Reasons) != 1 {
			t.Fatalf("label %d must carry exactly 1 Reason; got %+v", i, lbl.Reasons)
		}
		p, ok := lbl.Reasons[0].(diag.Provenance)
		if !ok {
			t.Fatalf("label %d Reasons[0] type = %T, want diag.Provenance", i, lbl.Reasons[0])
		}
		if p.Span.File != wantFiles[i] {
			t.Errorf("label %d span.File = %q, want %q (sorted)", i, p.Span.File, wantFiles[i])
		}
		if p.Span.Line <= 0 {
			t.Errorf("label %d span.Line = %d, want >0", i, p.Span.Line)
		}
	}
}

// Test 4: invalid positions (e.g., conjuncts synthesised with token.NoPos)
// must be filtered silently — no panic, no Provenance entry for them.
func TestProvenance_InvalidPosition_FilteredSilently(t *testing.T) {
	ctx := cuecontext.New()
	// Compile a value without a filename. The default filename is "" which
	// differs from host.cue; but Pos().IsValid() may still be true for
	// ambient positions. The helper must tolerate empty File strings.
	val := ctx.CompileString(`{x: int & >=5}`) // no cue.Filename
	if err := val.Err(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	x := val.LookupPath(cue.MakePath(cue.Str("x")))

	// Must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	labels := evaluator.ProvenanceNotesForTest(x, "host.cue")
	// All conjuncts have File == "", which we treat as invalid provenance
	// (NF3 — don't surface file-less positions). Entries must be skipped.
	for _, lbl := range labels {
		for _, r := range lbl.Reasons {
			if p, ok := r.(diag.Provenance); ok {
				if p.Span.File == "" {
					t.Errorf("empty-file Span must be filtered; got Provenance=%+v", p)
				}
			}
		}
	}
}

// Test 5: multiple entries are ordered by (File, Line), alphabetical on file.
func TestProvenance_SortByFileThenLine(t *testing.T) {
	ctx := cuecontext.New()
	// Two files, two lines each — enough to prove both axes of the sort.
	val := ctx.CompileString(`{x: int}`, cue.Filename("host.cue"))
	if err := val.Err(); err != nil {
		t.Fatalf("compile host: %v", err)
	}
	b := ctx.CompileString(`{
	x: int & <=100
	_line3: int & >=50
	x:      _line3
}`, cue.Filename("b.cue"))
	if err := b.Err(); err != nil {
		t.Fatalf("compile b: %v", err)
	}
	a := ctx.CompileString(`{
	x: int & >=0
	_line3: int & <=1000
	x:      _line3
}`, cue.Filename("a.cue"))
	if err := a.Err(); err != nil {
		t.Fatalf("compile a: %v", err)
	}
	val = val.Unify(a).Unify(b)
	x := val.LookupPath(cue.MakePath(cue.Str("x")))

	labels := evaluator.ProvenanceNotesForTest(x, "host.cue")
	if len(labels) < 2 {
		t.Fatalf("want ≥2 entries, got %d: %+v", len(labels), labels)
	}
	// Verify strictly non-decreasing order by (File, Line).
	var prevFile string
	var prevLine int
	for i, lbl := range labels {
		p, ok := lbl.Reasons[0].(diag.Provenance)
		if !ok {
			t.Fatalf("label %d Reasons[0] type = %T", i, lbl.Reasons[0])
		}
		sp := p.Span
		if i > 0 {
			if sp.File < prevFile {
				t.Errorf("labels out of file order: %q after %q (i=%d)", sp.File, prevFile, i)
			}
			if sp.File == prevFile && sp.Line < prevLine {
				t.Errorf("same-file labels out of line order: line %d after %d in %q (i=%d)", sp.Line, prevLine, sp.File, i)
			}
		}
		prevFile, prevLine = sp.File, sp.Line
	}
}
