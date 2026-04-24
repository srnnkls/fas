package diag_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/srnnkls/quae/internal/diag"
)

// ---------------------------------------------------------------------------
// JSON renderer contract for diag.Diagnostic (F9 / T13).
//
// Schema (pinned):
//
//   {
//     "code":     "E0301",
//     "severity": "error" | "warning" | "note",
//     "title":    "leaf constraint failed",
//     "location": { "file": "...", "line": N, "col": N },
//     "primary":  Label,
//     "notes":    [ Label, ... ] | omitted when empty,
//     "help":     "..."         | omitted when empty
//   }
//
//   Label = { "span": Span, "len": N, "msg": "...", "reasons": [Reason,...] (omitted when empty) }
//   Span  = { "file": "...", "line": N, "col": N, "length": N }
//   Reason variants carry their T1 snake_case "type" tag.
//
// ND-JSON contract: RenderJSON returns a single JSON object followed by a
// trailing newline. RenderJSONStream writes one such object per diagnostic,
// producing newline-delimited JSON suitable for streaming consumers.
// ---------------------------------------------------------------------------

// jsonObject is a convenience decoded shape used by several tests that need
// to peek at top-level keys without pinning byte order of nested structures.
type jsonObject = map[string]json.RawMessage

// parseObj decodes a JSON object, failing the test on malformed input.
func parseObj(t *testing.T, b []byte) jsonObject {
	t.Helper()
	var o jsonObject
	if err := json.Unmarshal(b, &o); err != nil {
		t.Fatalf("decode JSON object: %v (input: %s)", err, b)
	}
	return o
}

// newTestPos mints a token.Pos inside a freshly-allocated token.File, so the
// JSON renderer can resolve file/line/col from Label.Pos. Line/col resolution
// follows token.File semantics: offset 0 is (1,1); AddLine bumps subsequent
// offsets to the next line.
func newTestPos(t *testing.T, filename string, offsets []int, target int) token.Pos {
	t.Helper()
	f := token.NewFile(filename, 0, 4096)
	for _, o := range offsets {
		f.AddLine(o)
	}
	return f.Pos(target, token.NoRelPos)
}

// ---------------------------------------------------------------------------
// #1 — One diagnostic → one JSON object on a single line (ND-JSON form).
// ---------------------------------------------------------------------------

func TestRenderJSON_SingleDiagnosticSingleLine(t *testing.T) {
	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: newTestPos(t, "rule.cue", []int{0}, 10),
			Len: 4,
			Msg: "got: \"ls\"",
		},
	}
	b := diag.RenderJSON(d)
	if !bytes.HasSuffix(b, []byte("\n")) {
		t.Fatalf("RenderJSON must end with newline for ND-JSON framing: %q", b)
	}
	// Exactly one newline — the trailing framing character.
	if n := bytes.Count(b, []byte("\n")); n != 1 {
		t.Fatalf("RenderJSON must produce exactly one newline (got %d): %q", n, b)
	}
	// Stripped of its trailing newline, the output must be valid JSON.
	trimmed := bytes.TrimRight(b, "\n")
	if !json.Valid(trimmed) {
		t.Fatalf("RenderJSON body is not valid JSON: %s", trimmed)
	}
}

// ---------------------------------------------------------------------------
// #1b — Multiple diagnostics via RenderJSONStream → one object per line.
// ---------------------------------------------------------------------------

func TestRenderJSONStream_MultipleDiagnosticsNDJSON(t *testing.T) {
	d1 := diag.Diagnostic{
		Code: "E0301", Severity: diag.SeverityError, Title: "leaf",
		Primary: diag.Label{Pos: newTestPos(t, "r.cue", []int{0}, 1), Len: 1, Msg: "a"},
	}
	d2 := diag.Diagnostic{
		Code: "E0401", Severity: diag.SeverityError, Title: "arm",
		Primary: diag.Label{Pos: newTestPos(t, "r.cue", []int{0}, 5), Len: 1, Msg: "b"},
	}
	var buf bytes.Buffer
	if err := diag.RenderJSONStream(&buf, []diag.Diagnostic{d1, d2}); err != nil {
		t.Fatalf("RenderJSONStream: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 ND-JSON lines, got %d: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is not valid JSON: %s", i, line)
		}
	}
}

// ---------------------------------------------------------------------------
// #2 — Schema: required top-level fields are present.
// ---------------------------------------------------------------------------

func TestRenderJSON_SchemaFields(t *testing.T) {
	d := diag.Diagnostic{
		Code:     "E0303",
		Severity: diag.SeverityError,
		Title:    "type mismatch",
		Primary: diag.Label{
			Pos: newTestPos(t, "retry.cue", []int{0}, 21),
			Len: 3,
			Msg: "expected int",
			Reasons: []diag.Reason{
				diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"five"`},
			},
		},
		Notes: []diag.Label{
			{
				Pos:     newTestPos(t, "retry.cue", []int{0}, 21),
				Len:     3,
				Msg:     "introduced here",
				Reasons: []diag.Reason{diag.Provenance{Span: diag.Span{File: "stdlib.cue", Line: 7, Col: 17, Length: 3}, Snippet: ">=0"}},
			},
		},
		Help: "no value of kind string can satisfy a constraint of kind int",
	}

	b := diag.RenderJSON(d)
	obj := parseObj(t, bytes.TrimRight(b, "\n"))

	for _, k := range []string{"code", "severity", "title", "location", "primary"} {
		if _, ok := obj[k]; !ok {
			t.Errorf("missing required top-level key %q in %s", k, b)
		}
	}
	// Optional keys present here because populated.
	for _, k := range []string{"notes", "help"} {
		if _, ok := obj[k]; !ok {
			t.Errorf("expected optional key %q to be present (populated): %s", k, b)
		}
	}

	// location sub-shape.
	loc := parseObj(t, obj["location"])
	for _, k := range []string{"file", "line", "col"} {
		if _, ok := loc[k]; !ok {
			t.Errorf("missing location key %q in %s", k, obj["location"])
		}
	}
	if s, _ := strconvUnquote(loc["file"]); s != "retry.cue" {
		t.Errorf("location.file = %q, want %q", s, "retry.cue")
	}

	// primary sub-shape: pos{line,col,len}, msg, reasons.
	primary := parseObj(t, obj["primary"])
	for _, k := range []string{"pos", "msg"} {
		if _, ok := primary[k]; !ok {
			t.Errorf("missing primary key %q in %s", k, obj["primary"])
		}
	}
	pos := parseObj(t, primary["pos"])
	for _, k := range []string{"line", "col", "len"} {
		if _, ok := pos[k]; !ok {
			t.Errorf("missing primary.pos key %q in %s", k, primary["pos"])
		}
	}
	if _, ok := primary["reasons"]; !ok {
		t.Errorf("expected primary.reasons for a Reason-bearing Label: %s", obj["primary"])
	}

	// notes is an array of Label-shaped objects.
	var notes []jsonObject
	if err := json.Unmarshal(obj["notes"], &notes); err != nil {
		t.Fatalf("notes not an array: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes length = %d, want 1", len(notes))
	}
	if _, ok := notes[0]["pos"]; !ok {
		t.Errorf("notes[0] missing pos: %v", notes[0])
	}
}

// strconvUnquote strips JSON quotes from a RawMessage representing a string.
// Tiny helper — trading a full strconv import for inline clarity.
func strconvUnquote(r json.RawMessage) (string, error) {
	var s string
	err := json.Unmarshal(r, &s)
	return s, err
}

// ---------------------------------------------------------------------------
// #3 — Round-trip: marshal → unmarshal → remarshal byte-identical, covering
// all 7 Reason variants inside a single Diagnostic's Primary + Notes.
// ---------------------------------------------------------------------------

func TestRenderJSON_RoundTripAllReasonVariants(t *testing.T) {
	// Build a diagnostic whose Labels collectively carry all 7 Reason
	// variants — one Label in Primary.Reasons per variant where possible,
	// with the remaining wrapped in Notes.
	pos := newTestPos(t, "r.cue", []int{0}, 1)

	d := diag.Diagnostic{
		Code:     "E0303",
		Severity: diag.SeverityError,
		Title:    "all variants",
		Primary: diag.Label{
			Pos: pos,
			Len: 3,
			Msg: "primary",
			Reasons: []diag.Reason{
				diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"x"`},
				diag.BoundViolation{Op: ">=", Bound: "5", Actual: "3", Distance: "off by 2"},
				diag.RegexMismatch{Pattern: "^rm ", Input: "ls", DivergeAt: 0},
				diag.ConjunctFailed{
					Expr: ">=5",
					Span: diag.Span{File: "r.cue", Line: 1, Col: 1, Length: 3},
					Sub:  diag.BoundViolation{Op: ">=", Bound: "5", Actual: "3", Distance: "off by 2"},
				},
				diag.DisjunctionFailed{Arms: []diag.ArmResult{
					{
						Arm:   `"Read"`,
						Span:  diag.Span{File: "r.cue", Line: 1, Col: 1, Length: 6},
						Inner: diag.KindMismatch{Want: cue.StringKind, Got: cue.StringKind, Actual: `"Rd"`},
						Score: 50,
					},
				}},
				diag.KeyMissing{Key: "flags", AvailableKeys: []string{"flag"}, Suggestion: "flag"},
			},
		},
		Notes: []diag.Label{
			{
				Pos: pos,
				Len: 3,
				Msg: "note",
				Reasons: []diag.Reason{
					diag.Provenance{Span: diag.Span{File: "stdlib.cue", Line: 7, Col: 17, Length: 3}, Snippet: ">=0"},
				},
			},
		},
		Help: "help",
	}

	first := diag.RenderJSON(d)
	// Strip ND-JSON framing before unmarshal.
	body := bytes.TrimRight(first, "\n")

	var decoded diag.Diagnostic
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal Diagnostic: %v (input: %s)", err, body)
	}

	second := diag.RenderJSON(decoded)
	if !bytes.Equal(first, second) {
		t.Errorf("round-trip not byte-identical\n 1st: %s\n 2nd: %s", first, second)
	}
}

// ---------------------------------------------------------------------------
// #4 — All 7 Reason variants produce distinct snake_case "type" tags.
// ---------------------------------------------------------------------------

func TestRenderJSON_SevenReasonVariantsDistinctTags(t *testing.T) {
	pos := newTestPos(t, "r.cue", []int{0}, 1)
	d := diag.Diagnostic{
		Code: "E0000", Severity: diag.SeverityError, Title: "t",
		Primary: diag.Label{
			Pos: pos, Len: 1,
			Reasons: []diag.Reason{
				diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"x"`},
				diag.BoundViolation{Op: ">=", Bound: "5", Actual: "3", Distance: "off by 2"},
				diag.RegexMismatch{Pattern: "^rm ", Input: "ls", DivergeAt: 0},
				diag.ConjunctFailed{Expr: ">=5", Span: diag.Span{File: "r.cue", Line: 1, Col: 1, Length: 3},
					Sub: diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"x"`}},
				diag.DisjunctionFailed{Arms: []diag.ArmResult{}},
				diag.KeyMissing{Key: "k", AvailableKeys: []string{}, Suggestion: ""},
				diag.Provenance{Span: diag.Span{File: "f.cue", Line: 1, Col: 1, Length: 1}, Snippet: "x"},
			},
		},
	}

	body := bytes.TrimRight(diag.RenderJSON(d), "\n")
	obj := parseObj(t, body)
	primary := parseObj(t, obj["primary"])

	var reasons []jsonObject
	if err := json.Unmarshal(primary["reasons"], &reasons); err != nil {
		t.Fatalf("primary.reasons not an array: %v", err)
	}
	wantTags := []string{
		"kind_mismatch", "bound_violation", "regex_mismatch",
		"conjunct_failed", "disjunction_failed", "key_missing", "provenance",
	}
	if len(reasons) != len(wantTags) {
		t.Fatalf("reasons length = %d, want %d", len(reasons), len(wantTags))
	}
	seen := map[string]struct{}{}
	for i, r := range reasons {
		var tag string
		if err := json.Unmarshal(r["type"], &tag); err != nil {
			t.Fatalf("reason[%d].type: %v", i, err)
		}
		if tag != wantTags[i] {
			t.Errorf("reason[%d].type = %q, want %q", i, tag, wantTags[i])
		}
		if _, dup := seen[tag]; dup {
			t.Errorf("duplicate tag %q at position %d", tag, i)
		}
		seen[tag] = struct{}{}
	}
	if len(seen) != 7 {
		t.Errorf("distinct tags count = %d, want 7", len(seen))
	}
}

// ---------------------------------------------------------------------------
// #5 — Empty/nil Reasons slice is omitted from a Label's JSON output.
//
// Rationale: Label.MarshalJSON (reason_json.go) already uses omitempty for
// Reasons; the Diagnostic renderer must propagate that — primary.reasons
// must be *absent*, not "reasons":null or "reasons":[].
// ---------------------------------------------------------------------------

func TestRenderJSON_NilReasonsOmittedFromLabel(t *testing.T) {
	d := diag.Diagnostic{
		Code: "E0000", Severity: diag.SeverityError, Title: "t",
		Primary: diag.Label{
			Pos: newTestPos(t, "r.cue", []int{0}, 1),
			Len: 1,
			Msg: "no reasons",
			// Reasons deliberately nil.
		},
	}
	body := bytes.TrimRight(diag.RenderJSON(d), "\n")
	obj := parseObj(t, body)
	primary := parseObj(t, obj["primary"])
	if _, has := primary["reasons"]; has {
		t.Errorf("primary.reasons must be omitted when Reasons is nil; got: %s", obj["primary"])
	}
}

// ---------------------------------------------------------------------------
// #6 — Empty Notes/Help omitted. Both are optional; empty slice/string →
// omitted (not "notes":[] or "help":"") to keep the wire compact.
// ---------------------------------------------------------------------------

func TestRenderJSON_EmptyNotesAndHelpOmitted(t *testing.T) {
	d := diag.Diagnostic{
		Code: "E0301", Severity: diag.SeverityError, Title: "leaf",
		Primary: diag.Label{
			Pos: newTestPos(t, "r.cue", []int{0}, 1),
			Len: 1,
			Msg: "x",
		},
		// Notes nil, Help empty.
	}
	body := bytes.TrimRight(diag.RenderJSON(d), "\n")
	obj := parseObj(t, body)
	if _, has := obj["notes"]; has {
		t.Errorf("notes must be omitted when empty; got: %s", body)
	}
	if _, has := obj["help"]; has {
		t.Errorf("help must be omitted when empty; got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// #7 — Severity is a stable string.
// ---------------------------------------------------------------------------

func TestRenderJSON_SeverityIsStableString(t *testing.T) {
	cases := []struct {
		sev  diag.Severity
		want string
	}{
		{diag.SeverityError, "error"},
		{diag.SeverityWarning, "warning"},
		{diag.SeverityNote, "note"},
	}
	for _, c := range cases {
		d := diag.Diagnostic{
			Code: "E0000", Severity: c.sev, Title: "t",
			Primary: diag.Label{Pos: newTestPos(t, "r.cue", []int{0}, 1), Len: 1, Msg: "x"},
		}
		body := bytes.TrimRight(diag.RenderJSON(d), "\n")
		obj := parseObj(t, body)
		var got string
		if err := json.Unmarshal(obj["severity"], &got); err != nil {
			t.Fatalf("severity not a string for %v: %v", c.sev, err)
		}
		if got != c.want {
			t.Errorf("severity for %v = %q, want %q", c.sev, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// #8 — Provenance Reason inside a Note serializes correctly; the nested Span
// carries file/line/col through unchanged.
// ---------------------------------------------------------------------------

func TestRenderJSON_ProvenanceInNote(t *testing.T) {
	d := diag.Diagnostic{
		Code: "E0303", Severity: diag.SeverityError, Title: "type mismatch",
		Primary: diag.Label{
			Pos: newTestPos(t, "r.cue", []int{0}, 1),
			Len: 1,
			Msg: "x",
		},
		Notes: []diag.Label{
			{
				Pos: newTestPos(t, "r.cue", []int{0}, 1),
				Len: 1,
				Msg: "prov",
				Reasons: []diag.Reason{
					diag.Provenance{
						Span:    diag.Span{File: "stdlib/nums.cue", Line: 7, Col: 17, Length: 3},
						Snippet: ">=0",
					},
				},
			},
		},
	}
	body := bytes.TrimRight(diag.RenderJSON(d), "\n")
	obj := parseObj(t, body)
	var notes []jsonObject
	if err := json.Unmarshal(obj["notes"], &notes); err != nil {
		t.Fatalf("notes not an array: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes length = %d, want 1", len(notes))
	}
	var reasons []jsonObject
	if err := json.Unmarshal(notes[0]["reasons"], &reasons); err != nil {
		t.Fatalf("notes[0].reasons not an array: %v", err)
	}
	if len(reasons) != 1 {
		t.Fatalf("notes[0].reasons length = %d, want 1", len(reasons))
	}
	var tag string
	if err := json.Unmarshal(reasons[0]["type"], &tag); err != nil {
		t.Fatalf("note reason type: %v", err)
	}
	if tag != "provenance" {
		t.Errorf("note reason type = %q, want %q", tag, "provenance")
	}
	// Nested span carries the stdlib location.
	span := parseObj(t, reasons[0]["span"])
	if s, _ := strconvUnquote(span["file"]); s != "stdlib/nums.cue" {
		t.Errorf("provenance span.file = %q, want %q", s, "stdlib/nums.cue")
	}
}

// ---------------------------------------------------------------------------
// Key-ordering sanity: top-level keys appear in the struct-declared order
// (code, severity, title, location, primary, notes, help). Encoding/json
// preserves struct field order; this test pins that expectation so a future
// refactor that reorders fields trips on the regression.
// ---------------------------------------------------------------------------

func TestRenderJSON_TopLevelKeyOrder(t *testing.T) {
	d := diag.Diagnostic{
		Code: "E0301", Severity: diag.SeverityError, Title: "leaf",
		Primary: diag.Label{Pos: newTestPos(t, "r.cue", []int{0}, 1), Len: 1, Msg: "x"},
		Notes:   []diag.Label{{Pos: newTestPos(t, "r.cue", []int{0}, 1), Len: 1, Msg: "n"}},
		Help:    "h",
	}
	body := bytes.TrimRight(diag.RenderJSON(d), "\n")
	want := []string{"code", "severity", "title", "location", "primary", "notes", "help"}
	got := topLevelKeyOrder(t, body)
	if !equalStrings(got, want) {
		t.Errorf("top-level key order = %v, want %v\nbody: %s", got, want, body)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// topLevelKeyOrder extracts the order of top-level JSON keys via a streaming
// decoder — sensitive to emission order without relying on map iteration.
func topLevelKeyOrder(t *testing.T, body []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		t.Fatalf("expected object start, got %v", tok)
	}
	var keys []string
	depth := 1
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		switch v := tok.(type) {
		case json.Delim:
			if v == '{' || v == '[' {
				depth++
			} else {
				depth--
				if depth == 0 {
					return keys
				}
			}
		case string:
			if depth == 1 {
				keys = append(keys, v)
				// Consume the value.
				var raw json.RawMessage
				if err := dec.Decode(&raw); err != nil {
					t.Fatalf("decode value for %q: %v", v, err)
				}
			}
		}
	}
}
