package diag_test

import (
	"encoding/json"
	"strings"
	"testing"

	"cuelang.org/go/cue/token"

	"github.com/srnnkls/fas/internal/diag"
)

// decodeSARIF parses the hand-rolled SARIF bytes into a generic map so tests
// can assert on structure without coupling to private struct types.
func decodeSARIF(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("SARIF did not parse as JSON: %v\n--- raw ---\n%s", err, raw)
	}
	return m
}

func firstResult(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	runs, ok := doc["runs"].([]any)
	if !ok || len(runs) == 0 {
		t.Fatalf("SARIF missing runs[0]; doc=%v", doc)
	}
	run, ok := runs[0].(map[string]any)
	if !ok {
		t.Fatalf("runs[0] not an object: %v", runs[0])
	}
	results, ok := run["results"].([]any)
	if !ok || len(results) == 0 {
		t.Fatalf("runs[0].results empty; run=%v", run)
	}
	r, ok := results[0].(map[string]any)
	if !ok {
		t.Fatalf("results[0] not an object: %v", results[0])
	}
	return r
}

// TestRenderSARIF_TopLevelShape: the emitted document must be valid JSON with
// required SARIF 2.1.0 keys: $schema, version=="2.1.0", runs[], runs[0].tool.
func TestRenderSARIF_TopLevelShape(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "kind mismatch",
		Primary:  diag.Label{Pos: pos, Len: 2, Msg: "bad"},
	}

	raw := diag.RenderSARIF([]diag.Diagnostic{d})
	doc := decodeSARIF(t, raw)

	if got := doc["$schema"]; got == nil || got == "" {
		t.Errorf("missing $schema; got %v", got)
	}
	if got, want := doc["version"], "2.1.0"; got != want {
		t.Errorf("version = %v, want %q", got, want)
	}
	runs, ok := doc["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs should be a single-element array; got %v", doc["runs"])
	}
	run := runs[0].(map[string]any)
	tool, ok := run["tool"].(map[string]any)
	if !ok {
		t.Fatalf("runs[0].tool missing or not an object: %v", run["tool"])
	}
	driver, ok := tool["driver"].(map[string]any)
	if !ok {
		t.Fatalf("runs[0].tool.driver missing: %v", tool)
	}
	if got, want := driver["name"], "fas"; got != want {
		t.Errorf("tool.driver.name = %v, want %q", got, want)
	}
	if _, ok := driver["version"].(string); !ok {
		t.Errorf("tool.driver.version must be a string; got %T %v", driver["version"], driver["version"])
	}
}

// TestRenderSARIF_OneResultPerDiagnostic: each Diagnostic becomes exactly one
// element in runs[0].results preserving input order.
func TestRenderSARIF_OneResultPerDiagnostic(t *testing.T) {
	pos1 := newPos(t, "a.cue", 0)
	pos2 := newPos(t, "b.cue", 0)
	diags := []diag.Diagnostic{
		{Code: "E0301", Severity: diag.SeverityError, Title: "first", Primary: diag.Label{Pos: pos1, Len: 1}},
		{Code: "E0302", Severity: diag.SeverityWarning, Title: "second", Primary: diag.Label{Pos: pos2, Len: 1}},
		{Code: "E0303", Severity: diag.SeverityNote, Title: "third", Primary: diag.Label{Pos: pos1, Len: 1}},
	}

	doc := decodeSARIF(t, diag.RenderSARIF(diags))
	runs := doc["runs"].([]any)
	results := runs[0].(map[string]any)["results"].([]any)
	if len(results) != len(diags) {
		t.Fatalf("results length = %d, want %d", len(results), len(diags))
	}

	codes := make([]string, len(results))
	for i, r := range results {
		rm := r.(map[string]any)
		codes[i] = rm["ruleId"].(string)
	}
	want := []string{"E0301", "E0302", "E0303"}
	for i, w := range want {
		if codes[i] != w {
			t.Errorf("results[%d].ruleId = %q, want %q", i, codes[i], w)
		}
	}
}

// TestRenderSARIF_LevelMapping: Severity maps to the SARIF level vocabulary.
func TestRenderSARIF_LevelMapping(t *testing.T) {
	cases := []struct {
		sev  diag.Severity
		want string
	}{
		{diag.SeverityError, "error"},
		{diag.SeverityWarning, "warning"},
		{diag.SeverityNote, "note"},
	}
	for _, tc := range cases {
		pos := newPos(t, "r.cue", 0)
		d := diag.Diagnostic{
			Code:     "E0301",
			Severity: tc.sev,
			Title:    "x",
			Primary:  diag.Label{Pos: pos, Len: 1},
		}
		doc := decodeSARIF(t, diag.RenderSARIF([]diag.Diagnostic{d}))
		r := firstResult(t, doc)
		if got := r["level"]; got != tc.want {
			t.Errorf("severity %v → level %v, want %q", tc.sev, got, tc.want)
		}
	}
}

// TestRenderSARIF_RuleIDAndMessage: ruleId is the fas code and message.text
// carries the title (plus primary label Msg when present).
func TestRenderSARIF_RuleIDAndMessage(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "kind mismatch",
		Primary:  diag.Label{Pos: pos, Len: 2, Msg: "want: string, got: 1"},
	}
	doc := decodeSARIF(t, diag.RenderSARIF([]diag.Diagnostic{d}))
	r := firstResult(t, doc)

	if got, want := r["ruleId"], "E0301"; got != want {
		t.Errorf("ruleId = %v, want %q", got, want)
	}
	msg, ok := r["message"].(map[string]any)
	if !ok {
		t.Fatalf("message missing: %v", r["message"])
	}
	text, _ := msg["text"].(string)
	if !strings.Contains(text, "kind mismatch") {
		t.Errorf("message.text must contain title; got %q", text)
	}
	if !strings.Contains(text, "want: string, got: 1") {
		t.Errorf("message.text must contain primary label msg; got %q", text)
	}
}

// TestRenderSARIF_PrimaryLocation: the primary span becomes a physicalLocation
// with artifactLocation.uri + region {startLine, startColumn, charLength}.
func TestRenderSARIF_PrimaryLocation(t *testing.T) {
	f := token.NewFile("policies/git.cue", 0, 4096)
	pos := f.Pos(42, token.NoRelPos)
	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "key not found",
		Primary:  diag.Label{Pos: pos, Len: 5, Msg: "no such key"},
	}
	doc := decodeSARIF(t, diag.RenderSARIF([]diag.Diagnostic{d}))
	r := firstResult(t, doc)

	locs, ok := r["locations"].([]any)
	if !ok || len(locs) != 1 {
		t.Fatalf("locations must be a single-element array; got %v", r["locations"])
	}
	pl, ok := locs[0].(map[string]any)["physicalLocation"].(map[string]any)
	if !ok {
		t.Fatalf("locations[0].physicalLocation missing: %v", locs[0])
	}
	art, ok := pl["artifactLocation"].(map[string]any)
	if !ok {
		t.Fatalf("artifactLocation missing: %v", pl)
	}
	if got, want := art["uri"], "policies/git.cue"; got != want {
		t.Errorf("artifactLocation.uri = %v, want %q", got, want)
	}
	region, ok := pl["region"].(map[string]any)
	if !ok {
		t.Fatalf("region missing: %v", pl)
	}
	// startLine/startColumn must be ≥1; charLength == Label.Len.
	startLine, _ := region["startLine"].(float64)
	startCol, _ := region["startColumn"].(float64)
	charLen, _ := region["charLength"].(float64)
	if startLine < 1 {
		t.Errorf("region.startLine = %v, want ≥1", startLine)
	}
	if startCol < 1 {
		t.Errorf("region.startColumn = %v, want ≥1", startCol)
	}
	if int(charLen) != 5 {
		t.Errorf("region.charLength = %v, want 5", charLen)
	}
}

// TestRenderSARIF_RelatedLocations_Notes: note Labels become relatedLocations.
func TestRenderSARIF_RelatedLocations_Notes(t *testing.T) {
	primary := newPos(t, "r.cue", 0)
	note := newPos(t, "r.cue", 100)
	d := diag.Diagnostic{
		Code:     "E0401",
		Severity: diag.SeverityError,
		Title:    "disjunction failed",
		Primary:  diag.Label{Pos: primary, Len: 1, Msg: "bad"},
		Notes:    []diag.Label{{Pos: note, Len: 3, Msg: "see also"}},
	}
	doc := decodeSARIF(t, diag.RenderSARIF([]diag.Diagnostic{d}))
	r := firstResult(t, doc)

	rel, ok := r["relatedLocations"].([]any)
	if !ok || len(rel) == 0 {
		t.Fatalf("relatedLocations missing or empty: %v", r["relatedLocations"])
	}
	m := rel[0].(map[string]any)
	msg, _ := m["message"].(map[string]any)
	if got, _ := msg["text"].(string); got != "see also" {
		t.Errorf("relatedLocations[0].message.text = %q, want %q", got, "see also")
	}
}

// TestRenderSARIF_Provenance_Role: Provenance entries appear in
// relatedLocations carrying properties.role = "definition".
func TestRenderSARIF_Provenance_Role(t *testing.T) {
	primary := newPos(t, "r.cue", 0)
	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "kind mismatch",
		Primary: diag.Label{
			Pos: primary, Len: 1, Msg: "bad",
			Reasons: []diag.Reason{
				diag.Provenance{
					Span:    diag.Span{File: "schema.cue", Line: 7, Col: 3, Length: 4},
					Snippet: "type: string",
				},
			},
		},
	}
	doc := decodeSARIF(t, diag.RenderSARIF([]diag.Diagnostic{d}))
	r := firstResult(t, doc)

	rel, ok := r["relatedLocations"].([]any)
	if !ok || len(rel) == 0 {
		t.Fatalf("relatedLocations missing: %v", r["relatedLocations"])
	}
	// find the Provenance entry
	var prov map[string]any
	for _, e := range rel {
		em := e.(map[string]any)
		props, _ := em["properties"].(map[string]any)
		if props != nil && props["role"] == "definition" {
			prov = em
			break
		}
	}
	if prov == nil {
		t.Fatalf("no relatedLocations entry with properties.role=definition; rel=%v", rel)
	}
	pl, _ := prov["physicalLocation"].(map[string]any)
	art, _ := pl["artifactLocation"].(map[string]any)
	if got, want := art["uri"], "schema.cue"; got != want {
		t.Errorf("provenance artifactLocation.uri = %v, want %q", got, want)
	}
	region, _ := pl["region"].(map[string]any)
	if sl, _ := region["startLine"].(float64); int(sl) != 7 {
		t.Errorf("provenance region.startLine = %v, want 7", sl)
	}
}

// TestRenderSARIF_InvalidPosition_OmitsLocation: a Diagnostic with NoPos /
// zero Span must render without a locations entry and without panicking.
func TestRenderSARIF_InvalidPosition_OmitsLocation(t *testing.T) {
	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "no pos",
		Primary:  diag.Label{Pos: token.NoPos, Len: 0, Msg: "where?"},
	}
	raw := diag.RenderSARIF([]diag.Diagnostic{d})
	doc := decodeSARIF(t, raw)
	r := firstResult(t, doc)

	if locs, ok := r["locations"]; ok {
		if arr, ok := locs.([]any); ok && len(arr) > 0 {
			t.Errorf("locations should be omitted when Primary has no valid Pos; got %v", locs)
		}
	}
}

// TestRenderSARIF_Deterministic: repeated renders of the same input produce
// byte-identical output (NF2).
func TestRenderSARIF_Deterministic(t *testing.T) {
	primary := newPos(t, "r.cue", 0)
	note := newPos(t, "r.cue", 100)
	d := diag.Diagnostic{
		Code: "E0301", Severity: diag.SeverityError, Title: "x",
		Primary: diag.Label{Pos: primary, Len: 2, Msg: "a"},
		Notes:   []diag.Label{{Pos: note, Len: 1, Msg: "b"}},
	}
	first := diag.RenderSARIF([]diag.Diagnostic{d})
	for range 50 {
		if got := diag.RenderSARIF([]diag.Diagnostic{d}); string(got) != string(first) {
			t.Fatalf("non-deterministic SARIF:\nfirst:\n%s\nlater:\n%s", first, got)
		}
	}
}

// TestRenderSARIF_EmptyDiags: zero diagnostics still emit a valid SARIF
// document with an empty results array (not null).
func TestRenderSARIF_EmptyDiags(t *testing.T) {
	raw := diag.RenderSARIF(nil)
	doc := decodeSARIF(t, raw)
	runs := doc["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs should have exactly 1 entry even for empty diags; got %d", len(runs))
	}
	results, ok := runs[0].(map[string]any)["results"].([]any)
	if !ok {
		t.Fatalf("results must be an array (possibly empty); got %T %v", runs[0].(map[string]any)["results"], runs[0].(map[string]any)["results"])
	}
	if len(results) != 0 {
		t.Errorf("results should be empty; got %d entries", len(results))
	}
}
