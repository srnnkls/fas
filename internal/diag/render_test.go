package diag_test

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/token"

	"github.com/srnnkls/quae/internal/diag"
)

// fakeSource is a deterministic SourceCache for tests. It returns whatever
// was registered for a given token.Pos, or ok=false if the pos is unknown.
//
// Keying on token.Pos directly keeps test setup trivial: build a token.File,
// pick positions via file.Pos(offset, token.NoRelPos), register lines, then
// feed the same positions to Diagnostic.Primary / Notes.
type fakeSource struct {
	// entries maps a pos to the line snippet + 1-based line/col coordinates
	// the renderer should see. The renderer must not discover anything that
	// wasn't explicitly registered — that discipline pins the contract.
	entries map[token.Pos]fakeEntry
}

type fakeEntry struct {
	line    string
	lineNum int
	col     int
}

func (f fakeSource) LineAt(p token.Pos) (string, int, int, bool) {
	e, ok := f.entries[p]
	if !ok {
		return "", 0, 0, false
	}
	return e.line, e.lineNum, e.col, true
}

// newPos builds a synthetic token.Pos inside a freshly-minted token.File.
// Offsets are 0-based; the file is sized generously so any small offset is
// valid. Line/col values reported by the SourceCache are independent of the
// offset — the renderer consumes only what LineAt returns.
func newPos(t *testing.T, filename string, offset int) token.Pos {
	t.Helper()
	f := token.NewFile(filename, 0, 4096)
	return f.Pos(offset, token.NoRelPos)
}

// TestRender_SingleLabelGolden is the anchor test: a fixed Diagnostic plus a
// fake SourceCache must render byte-for-byte to a hard-coded string. This is
// the determinism contract (F1, NF2) and the shape contract in one test.
func TestRender_SingleLabelGolden(t *testing.T) {
	pos := newPos(t, "tests/policies/git.cue", 100)

	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {
			line:    "    tool_input: flags: force: true",
			lineNum: 12,
			col:     17,
		},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "key not found",
		Primary: diag.Label{
			Pos: pos,
			Len: 5,
			Msg: `key "flags" not found in input at path tool_input`,
		},
		Help: "input.tool_input has keys: command, file_path",
	}

	want := "error[E0201]: key not found\n" +
		"  --> tests/policies/git.cue:12:17\n" +
		"   |\n" +
		"12 |     tool_input: flags: force: true\n" +
		`   |                 ^^^^^ key "flags" not found in input at path tool_input` + "\n" +
		"   |\n" +
		"   = help: input.tool_input has keys: command, file_path\n"

	got := diag.Render(d, src)
	if got != want {
		t.Fatalf("Render mismatch.\n--- got ---\n%s\n--- want ---\n%s\n--- diff (bytes) ---\n%s",
			got, want, byteDiff(got, want))
	}
}

// TestRender_Deterministic proves the renderer is pure: repeated calls on the
// same inputs yield byte-identical outputs. Guards against map iteration
// order, time-dependent formatting, and other determinism leaks (NF2).
func TestRender_Deterministic(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "x: 1", lineNum: 1, col: 1},
	}}
	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "bad",
		Primary:  diag.Label{Pos: pos, Len: 1, Msg: "here"},
	}

	first := diag.Render(d, src)
	for range 50 {
		if got := diag.Render(d, src); got != first {
			t.Fatalf("non-deterministic render:\nfirst:\n%s\nlater:\n%s", first, got)
		}
	}
}

// TestRender_MultiLabel_SourceOrder: when a diagnostic carries several notes,
// the renderer prints them in source order (ascending line number) regardless
// of their order in the Notes slice. Assertions target the visual ordering
// rather than structural equality so the test pins user-visible behavior.
func TestRender_MultiLabel_SourceOrder(t *testing.T) {
	primary := newPos(t, "r.cue", 0)
	note1 := newPos(t, "r.cue", 100)
	note2 := newPos(t, "r.cue", 200)

	src := fakeSource{entries: map[token.Pos]fakeEntry{
		primary: {line: "first_line", lineNum: 3, col: 1},
		note1:   {line: "tenth_line", lineNum: 10, col: 5},
		note2:   {line: "seventh", lineNum: 7, col: 2},
	}}

	d := diag.Diagnostic{
		Code:     "E0401",
		Severity: diag.SeverityError,
		Title:    "multi",
		Primary:  diag.Label{Pos: primary, Len: 5, Msg: "primary here"},
		// Intentionally out of source order: note on line 10 listed before line 7.
		Notes: []diag.Label{
			{Pos: note1, Len: 4, Msg: "later"},
			{Pos: note2, Len: 3, Msg: "earlier"},
		},
	}

	got := diag.Render(d, src)

	// The primary (line 3) must come first, then line 7 (earlier note), then line 10.
	primaryIdx := strings.Index(got, "first_line")
	earlierIdx := strings.Index(got, "seventh")
	laterIdx := strings.Index(got, "tenth_line")

	if primaryIdx < 0 || earlierIdx < 0 || laterIdx < 0 {
		t.Fatalf("one or more snippet lines missing.\noutput:\n%s", got)
	}
	if primaryIdx >= earlierIdx || earlierIdx >= laterIdx {
		t.Fatalf("labels not rendered in source order.\nprimary@%d earlier@%d later@%d\noutput:\n%s",
			primaryIdx, earlierIdx, laterIdx, got)
	}

	// Each note's message must appear on its caret line.
	if !strings.Contains(got, "earlier") {
		t.Errorf("missing earlier note message")
	}
	if !strings.Contains(got, "later") {
		t.Errorf("missing later note message")
	}
}

// TestRender_MissingSource_Degrades: when SourceCache returns ok=false, the
// renderer must emit a degraded label (no snippet, no caret) rather than
// panic. Output must still identify the diagnostic code and title so the
// user gets something actionable (NF3).
func TestRender_MissingSource_Degrades(t *testing.T) {
	// Pos intentionally NOT registered in fakeSource; LineAt will return ok=false.
	pos := newPos(t, "vanished.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "key not found",
		Primary:  diag.Label{Pos: pos, Len: 3, Msg: "no source"},
	}

	var got string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Render panicked on missing source: %v", r)
			}
		}()
		got = diag.Render(d, src)
	}()

	// Must contain the header.
	if !strings.Contains(got, "error[E0201]: key not found") {
		t.Errorf("output missing header.\noutput:\n%s", got)
	}
	// Must contain the degraded marker (NF3 calls it "position unknown").
	if !strings.Contains(got, "position unknown") {
		t.Errorf("degraded output missing 'position unknown' marker.\noutput:\n%s", got)
	}
	// Must NOT try to render a caret line with ^ under nothing.
	if strings.Contains(got, "^") {
		t.Errorf("degraded output should not contain caret characters.\noutput:\n%s", got)
	}
}

// TestRender_ZeroWidthLabel: Len=0 must render a single-column caret (one ^)
// rather than zero carets or a sequence. This is the convention for
// point-style diagnostics (insertion points, EOF errors).
func TestRender_ZeroWidthLabel(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "abc", lineNum: 1, col: 2},
	}}

	d := diag.Diagnostic{
		Code:     "E0202",
		Severity: diag.SeverityError,
		Title:    "point",
		Primary:  diag.Label{Pos: pos, Len: 0, Msg: "here"},
	}

	got := diag.Render(d, src)

	// Extract the caret line — the one that contains `^` after the gutter `|`.
	caretLine := findCaretLine(t, got)

	// Count consecutive ^ characters.
	caretCount := strings.Count(caretLine, "^")
	if caretCount != 1 {
		t.Errorf("Len=0 should render exactly one caret (^), got %d.\ncaret line:\n%s\nfull output:\n%s",
			caretCount, caretLine, got)
	}
}

// TestRender_WidthLabel: Len=N must render N carets (^^^^ for Len=4). Paired
// with TestRender_ZeroWidthLabel this pins the caret-width contract.
func TestRender_WidthLabel(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "abcdefgh", lineNum: 1, col: 3},
	}}

	d := diag.Diagnostic{
		Code:     "E0303",
		Severity: diag.SeverityError,
		Title:    "span",
		Primary:  diag.Label{Pos: pos, Len: 4, Msg: "wide"},
	}

	got := diag.Render(d, src)
	caretLine := findCaretLine(t, got)

	if strings.Count(caretLine, "^") != 4 {
		t.Errorf("Len=4 should render four carets. caret line:\n%s\nfull:\n%s", caretLine, got)
	}
	// Four consecutive carets should appear as a run.
	if !strings.Contains(caretLine, "^^^^") {
		t.Errorf("Len=4 carets should be contiguous. caret line:\n%s", caretLine)
	}
}

// TestRender_SeverityHeader: each severity uses the documented banner text.
// Error codes are shared across severities — the banner word distinguishes
// them in CLI output and scrut snapshots.
func TestRender_SeverityHeader(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "x: 1", lineNum: 1, col: 1},
	}}

	cases := []struct {
		name     string
		severity diag.Severity
		code     string
		want     string
	}{
		{"error", diag.SeverityError, "E0201", "error[E0201]: t"},
		{"warning", diag.SeverityWarning, "E0301", "warning[E0301]: t"},
		{"note", diag.SeverityNote, "E0401", "note[E0401]: t"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := diag.Diagnostic{
				Code:     tc.code,
				Severity: tc.severity,
				Title:    "t",
				Primary:  diag.Label{Pos: pos, Len: 1, Msg: "m"},
			}
			got := diag.Render(d, src)
			// First line only: banner lives on the header.
			first, _, _ := strings.Cut(got, "\n")
			if first != tc.want {
				t.Errorf("header mismatch.\n  got: %q\n want: %q", first, tc.want)
			}
		})
	}
}

// TestRender_OmitsHelpWhenEmpty: an empty Help string must not produce a
// `= help:` trailing line. Avoids a dangling empty-help footer on E-codes
// that don't ship a remediation string.
func TestRender_OmitsHelpWhenEmpty(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "x: 1", lineNum: 1, col: 1},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "no help",
		Primary:  diag.Label{Pos: pos, Len: 1, Msg: "m"},
		// Help intentionally empty.
	}

	got := diag.Render(d, src)

	if strings.Contains(got, "= help:") {
		t.Errorf("empty Help should not render `= help:` line.\noutput:\n%s", got)
	}
	if strings.Contains(got, "help:") {
		// Stronger — in case the renderer emits `help:` under a different banner.
		t.Errorf("empty Help should not render any help-prefixed line.\noutput:\n%s", got)
	}
}

// TestRender_EmitsHelpWhenPresent is the positive of the above: a non-empty
// Help string renders a `= help: ...` line at the tail.
func TestRender_EmitsHelpWhenPresent(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "x: 1", lineNum: 1, col: 1},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "with help",
		Primary:  diag.Label{Pos: pos, Len: 1, Msg: "m"},
		Help:     "try adding the key",
	}

	got := diag.Render(d, src)
	if !strings.Contains(got, "= help: try adding the key") {
		t.Errorf("missing help line.\noutput:\n%s", got)
	}
}

// TestRender_LocationLine: the `--> file:line:col` line carries the exact
// file name and 1-based coordinates returned by the SourceCache. Column
// alignment is pinned to the SourceCache's col value, not derived from Pos.
func TestRender_LocationLine(t *testing.T) {
	pos := newPos(t, "a/b/c.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "hello", lineNum: 42, col: 7},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "t",
		Primary:  diag.Label{Pos: pos, Len: 1, Msg: "m"},
	}

	got := diag.Render(d, src)
	if !strings.Contains(got, "  --> a/b/c.cue:42:7") {
		t.Errorf("expected location line `  --> a/b/c.cue:42:7`.\noutput:\n%s", got)
	}
}

// TestRender_GutterAlignsToMaxLineNumber: the left gutter width must match
// the widest line number in the diagnostic so all snippet lines align. With
// a primary on line 3 and a note on line 123, the gutter must be 3 chars wide.
func TestRender_GutterAlignsToMaxLineNumber(t *testing.T) {
	primary := newPos(t, "r.cue", 0)
	note := newPos(t, "r.cue", 100)

	src := fakeSource{entries: map[token.Pos]fakeEntry{
		primary: {line: "first", lineNum: 3, col: 1},
		note:    {line: "later", lineNum: 123, col: 1},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "t",
		Primary:  diag.Label{Pos: primary, Len: 1, Msg: "p"},
		Notes:    []diag.Label{{Pos: note, Len: 1, Msg: "n"}},
	}

	got := diag.Render(d, src)
	lines := strings.SplitSeq(got, "\n")

	// Find every line starting with a digit — those are snippet lines with
	// the line number in the gutter.
	for line := range lines {
		// Look for the snippet lines by prefix pattern.
		if strings.HasPrefix(line, "3 |") {
			t.Errorf("line 3 snippet should be left-padded to align with 3-digit gutter, got %q", line)
		}
	}
	// Positive check: the 3-wide gutter must render as "  3 |" for line 3
	// and "123 |" for line 123.
	if !strings.Contains(got, "  3 | first") {
		t.Errorf("expected `  3 | first` with 3-wide gutter.\noutput:\n%s", got)
	}
	if !strings.Contains(got, "123 | later") {
		t.Errorf("expected `123 | later`.\noutput:\n%s", got)
	}
}

// TestRender_CaretAlignsToColumn: the caret(s) under a snippet must start at
// the 1-based column reported by SourceCache. Given col=5 and Len=3, the
// caret line must have exactly 4 spaces between `| ` and `^^^`.
func TestRender_CaretAlignsToColumn(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "abcdefgh", lineNum: 1, col: 5},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "t",
		Primary:  diag.Label{Pos: pos, Len: 3, Msg: "m"},
	}

	got := diag.Render(d, src)
	caretLine := findCaretLine(t, got)

	// The caret line looks like: "   |    ^^^ m" with the gutter, then spaces
	// matching column 5 (= 4 leading spaces), then 3 carets.
	// Find the `|` in the gutter, advance one space, then count spaces before ^.
	pipeIdx := strings.Index(caretLine, "|")
	if pipeIdx < 0 {
		t.Fatalf("caret line has no gutter pipe: %q", caretLine)
	}
	after := caretLine[pipeIdx+1:]
	caretIdx := strings.Index(after, "^")
	if caretIdx < 0 {
		t.Fatalf("caret line has no caret character: %q", caretLine)
	}
	// After `|`, renderer emits one space + (col-1) spaces before caret.
	// So total spaces between `|` and `^` = col.
	wantSpaces := 5 // col=5 → 5 chars between `|` and `^`
	if caretIdx != wantSpaces {
		t.Errorf("caret misaligned: got %d spaces between `|` and `^`, want %d.\ncaret line: %q",
			caretIdx, wantSpaces, caretLine)
	}
}

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// findCaretLine returns the line in out that begins with the gutter and
// contains carets — i.e. the line immediately following a snippet line. Fails
// the test if no such line exists.
func findCaretLine(t *testing.T, out string) string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "^") && strings.Contains(line, "|") {
			return line
		}
	}
	t.Fatalf("no caret line found in output:\n%s", out)
	return ""
}

// byteDiff highlights the first differing byte between two strings so the
// golden assertion error reports the mismatch location rather than just
// dumping both strings.
func byteDiff(got, want string) string {
	minLen := min(len(want), len(got))
	for i := range minLen {
		if got[i] != want[i] {
			return "first diff at byte " + itoa(i) +
				": got=" + quoteByte(got[i]) +
				" want=" + quoteByte(want[i])
		}
	}
	if len(got) != len(want) {
		return "length differs: got=" + itoa(len(got)) + " want=" + itoa(len(want))
	}
	return "(no diff detected)"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}

func quoteByte(b byte) string {
	if b >= 32 && b < 127 {
		return "'" + string(b) + "'"
	}
	return "0x" + hex(b)
}

func hex(b byte) string {
	const h = "0123456789abcdef"
	return string([]byte{h[b>>4], h[b&0xf]})
}
