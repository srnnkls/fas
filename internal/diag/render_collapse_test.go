package diag_test

import (
	"strings"
	"testing"

	"cuelang.org/go/cue/token"

	"github.com/srnnkls/fas/internal/diag"
)

// TestRender_Collapse_ThreeLabelsSameSpan: a Primary plus two Notes on the
// exact same (Pos, Len) must render the source snippet and caret row exactly
// once, followed by three aligned message rows (primary + 2 notes).
func TestRender_Collapse_ThreeLabelsSameSpan(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "    tool_input: flags: force: true", lineNum: 10, col: 5},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "same span",
		Primary:  diag.Label{Pos: pos, Len: 3, Msg: "primary here"},
		Notes: []diag.Label{
			{Pos: pos, Len: 3, Msg: "first note"},
			{Pos: pos, Len: 3, Msg: "second note"},
		},
	}

	got := diag.Render(d, src)

	// The source snippet line must appear exactly once.
	if n := strings.Count(got, "tool_input: flags: force: true"); n != 1 {
		t.Errorf("source snippet should appear exactly once, got %d.\noutput:\n%s", n, got)
	}

	// The caret row "^^^" should appear exactly once.
	if n := strings.Count(got, "^^^"); n != 1 {
		t.Errorf("caret row `^^^` should appear exactly once, got %d.\noutput:\n%s", n, got)
	}

	// All three messages must appear.
	for _, msg := range []string{"primary here", "first note", "second note"} {
		if !strings.Contains(got, msg) {
			t.Errorf("missing message %q in output:\n%s", msg, got)
		}
	}

	// Messages must appear in Primary → Notes order.
	iPrimary := strings.Index(got, "primary here")
	iFirst := strings.Index(got, "first note")
	iSecond := strings.Index(got, "second note")
	if iPrimary >= iFirst || iFirst >= iSecond {
		t.Errorf("messages out of order: primary@%d first@%d second@%d\noutput:\n%s",
			iPrimary, iFirst, iSecond, got)
	}

	// All collapsed message rows must align under the caret column of the
	// first (Primary) occurrence. Locate the caret column in the primary's
	// caret row, and ensure subsequent message rows have their message text
	// starting at the same column.
	caretLine := findCaretLine(t, got)
	pipeIdx := strings.Index(caretLine, "|")
	if pipeIdx < 0 {
		t.Fatalf("caret line has no gutter pipe: %q", caretLine)
	}
	caretCol := strings.Index(caretLine[pipeIdx+1:], "^")
	if caretCol < 0 {
		t.Fatalf("caret line has no caret character: %q", caretLine)
	}

	// Aligned rows for the collapsed notes should have their message start
	// at the same offset after `|` as the carets.
	lines := strings.Split(got, "\n")
	var alignedRows []string
	for _, line := range lines {
		if strings.Contains(line, "first note") || strings.Contains(line, "second note") {
			alignedRows = append(alignedRows, line)
		}
	}
	if len(alignedRows) != 2 {
		t.Fatalf("expected 2 aligned message rows for notes, got %d.\noutput:\n%s",
			len(alignedRows), got)
	}
	for _, row := range alignedRows {
		// Must start with gutter `|` (no snippet-line number prefix, no
		// caret characters).
		if strings.Contains(row, "^") {
			t.Errorf("collapsed message row should not contain carets: %q", row)
		}
		_, after, ok := strings.Cut(row, "|")
		if !ok {
			t.Errorf("collapsed message row missing gutter pipe: %q", row)
			continue
		}
		rest := after
		// Skip the leading space after `|`, then find where the message text
		// starts (first non-space byte).
		msgStart := -1
		for i := 0; i < len(rest); i++ {
			if rest[i] != ' ' {
				msgStart = i
				break
			}
		}
		if msgStart < 0 {
			t.Errorf("collapsed message row has no message text: %q", row)
			continue
		}
		// Message should start at the same offset as the caret.
		if msgStart != caretCol {
			t.Errorf("collapsed message row misaligned: msg@%d caret@%d row=%q",
				msgStart, caretCol, row)
		}
	}
}

// TestRender_Collapse_DifferentSpansIndependent: two notes on different spans
// produce independent frames — each with its own source snippet and caret row.
func TestRender_Collapse_DifferentSpansIndependent(t *testing.T) {
	primary := newPos(t, "r.cue", 0)
	note1 := newPos(t, "r.cue", 100)
	note2 := newPos(t, "r.cue", 200)

	src := fakeSource{entries: map[token.Pos]fakeEntry{
		primary: {line: "alpha_line", lineNum: 1, col: 1},
		note1:   {line: "beta_line", lineNum: 2, col: 1},
		note2:   {line: "gamma_line", lineNum: 3, col: 1},
	}}

	d := diag.Diagnostic{
		Code:     "E0401",
		Severity: diag.SeverityError,
		Title:    "three spans",
		Primary:  diag.Label{Pos: primary, Len: 5, Msg: "p"},
		Notes: []diag.Label{
			{Pos: note1, Len: 4, Msg: "n1"},
			{Pos: note2, Len: 5, Msg: "n2"},
		},
	}

	got := diag.Render(d, src)

	// All three source lines must appear once each.
	for _, snippet := range []string{"alpha_line", "beta_line", "gamma_line"} {
		if n := strings.Count(got, snippet); n != 1 {
			t.Errorf("snippet %q should appear once, got %d.\noutput:\n%s",
				snippet, n, got)
		}
	}

	// Three distinct caret rows (one per frame).
	caretLineCount := 0
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "^") && strings.Contains(line, "|") {
			caretLineCount++
		}
	}
	if caretLineCount != 3 {
		t.Errorf("expected 3 caret rows for 3 independent frames, got %d.\noutput:\n%s",
			caretLineCount, got)
	}
}

// TestRender_Collapse_MixedCollapseAndIndependent: two notes share the primary's
// span and one note is on a different span. First frame emits snippet+caret
// once with 3 aligned messages; second frame emits its own snippet+caret+msg.
func TestRender_Collapse_MixedCollapseAndIndependent(t *testing.T) {
	primary := newPos(t, "r.cue", 0)
	sameAsPrimary1 := newPos(t, "r.cue", 1)
	sameAsPrimary2 := newPos(t, "r.cue", 2)
	other := newPos(t, "r.cue", 100)

	// Same (file, line, col, len) across primary and two notes — different
	// token.Pos identities, same source coordinates.
	same := fakeEntry{line: "target_line", lineNum: 5, col: 3}

	src := fakeSource{entries: map[token.Pos]fakeEntry{
		primary:        same,
		sameAsPrimary1: same,
		sameAsPrimary2: same,
		other:          {line: "other_line", lineNum: 9, col: 2},
	}}

	d := diag.Diagnostic{
		Code:     "E0303",
		Severity: diag.SeverityError,
		Title:    "mixed",
		Primary:  diag.Label{Pos: primary, Len: 4, Msg: "primary msg"},
		Notes: []diag.Label{
			{Pos: sameAsPrimary1, Len: 4, Msg: "note a"},
			{Pos: sameAsPrimary2, Len: 4, Msg: "note b"},
			{Pos: other, Len: 3, Msg: "note c"},
		},
	}

	got := diag.Render(d, src)

	// First frame's source appears once.
	if n := strings.Count(got, "target_line"); n != 1 {
		t.Errorf("collapsed frame snippet should appear once, got %d.\noutput:\n%s",
			n, got)
	}
	// Independent frame's source appears once.
	if n := strings.Count(got, "other_line"); n != 1 {
		t.Errorf("independent frame snippet should appear once, got %d.\noutput:\n%s",
			n, got)
	}

	// All four messages present.
	for _, msg := range []string{"primary msg", "note a", "note b", "note c"} {
		if !strings.Contains(got, msg) {
			t.Errorf("missing message %q in output:\n%s", msg, got)
		}
	}

	// Exactly 2 caret rows: one for the collapsed frame, one for the
	// independent frame.
	caretRows := 0
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "^") && strings.Contains(line, "|") {
			caretRows++
		}
	}
	if caretRows != 2 {
		t.Errorf("expected 2 caret rows (1 collapsed + 1 independent), got %d.\noutput:\n%s",
			caretRows, got)
	}

	// Ordering: primary msg → note a → note b (all in first frame), then
	// note c (second frame, after other_line).
	idxPrimary := strings.Index(got, "primary msg")
	idxA := strings.Index(got, "note a")
	idxB := strings.Index(got, "note b")
	idxOther := strings.Index(got, "other_line")
	idxC := strings.Index(got, "note c")

	if idxPrimary >= idxA || idxA >= idxB || idxB >= idxOther || idxOther >= idxC {
		t.Errorf("messages/snippets out of order:\nprimary@%d a@%d b@%d other@%d c@%d\noutput:\n%s",
			idxPrimary, idxA, idxB, idxOther, idxC, got)
	}
}

// TestRender_Collapse_TupleEquality: collapse is triggered by (file, line, col,
// len) equality, not by token.Pos identity. Differing on any of those fields
// produces an independent frame. This sanity-tests the collapse key.
func TestRender_Collapse_TupleEquality(t *testing.T) {
	cases := []struct {
		name             string
		note             fakeEntry
		noteLen          int
		wantCollapse     bool // true → 1 caret row, false → 2 caret rows
		wantSnippetTwice bool // source appears twice when not collapsed
	}{
		{
			name:             "same file line col len collapses",
			note:             fakeEntry{line: "x", lineNum: 7, col: 4},
			noteLen:          3,
			wantCollapse:     true,
			wantSnippetTwice: false,
		},
		{
			name:             "differing line does not collapse",
			note:             fakeEntry{line: "y", lineNum: 8, col: 4},
			noteLen:          3,
			wantCollapse:     false,
			wantSnippetTwice: false, // different line, different snippet line content
		},
		{
			name:             "differing col does not collapse",
			note:             fakeEntry{line: "x", lineNum: 7, col: 5},
			noteLen:          3,
			wantCollapse:     false,
			wantSnippetTwice: true, // same file/line, different col → frame re-emits
		},
		{
			name:             "differing len does not collapse",
			note:             fakeEntry{line: "x", lineNum: 7, col: 4},
			noteLen:          4,
			wantCollapse:     false,
			wantSnippetTwice: true, // same file/line/col, different len
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			primary := newPos(t, "r.cue", 0)
			note := newPos(t, "r.cue", 500)

			src := fakeSource{entries: map[token.Pos]fakeEntry{
				primary: {line: "x", lineNum: 7, col: 4},
				note:    tc.note,
			}}

			d := diag.Diagnostic{
				Code:     "E0303",
				Severity: diag.SeverityError,
				Title:    "tuple",
				Primary:  diag.Label{Pos: primary, Len: 3, Msg: "p"},
				Notes:    []diag.Label{{Pos: note, Len: tc.noteLen, Msg: "n"}},
			}

			got := diag.Render(d, src)

			caretRows := 0
			for line := range strings.SplitSeq(got, "\n") {
				if strings.Contains(line, "^") && strings.Contains(line, "|") {
					caretRows++
				}
			}

			wantCarets := 2
			if tc.wantCollapse {
				wantCarets = 1
			}
			if caretRows != wantCarets {
				t.Errorf("caret row count: got %d, want %d.\noutput:\n%s",
					caretRows, wantCarets, got)
			}
		})
	}
}

// TestRender_Collapse_CaretAlignmentTabs: when the source line has leading
// tabs, a collapsed note's aligned message row must line up under the same
// expanded visual caret column as the primary.
func TestRender_Collapse_CaretAlignmentTabs(t *testing.T) {
	primary := newPos(t, "r.cue", 0)
	note := newPos(t, "r.cue", 100)

	// Same source coordinates: tab-indented line, col=3.
	same := fakeEntry{line: "\t\tfoo: bar", lineNum: 1, col: 3}

	src := fakeSource{entries: map[token.Pos]fakeEntry{
		primary: same,
		note:    same,
	}}

	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "tabs",
		Primary:  diag.Label{Pos: primary, Len: 3, Msg: "primary msg"},
		Notes:    []diag.Label{{Pos: note, Len: 3, Msg: "collapsed note"}},
	}

	got := diag.Render(d, src)

	// Source should appear once; one caret row.
	if n := strings.Count(got, "foo: bar"); n != 1 {
		t.Errorf("snippet should appear once, got %d.\noutput:\n%s", n, got)
	}
	caretLine := findCaretLine(t, got)

	// Determine the expanded caret column (byte col 3 with 2 leading tabs →
	// visual col 3 + 2*3 = 9).
	pipeIdx := strings.Index(caretLine, "|")
	after := caretLine[pipeIdx+1:]
	caretCol := strings.Index(after, "^")
	if caretCol != 9 {
		t.Errorf("caret column after tab expansion: got %d, want 9.\ncaret line: %q",
			caretCol, caretLine)
	}

	// The collapsed note message row must place "collapsed note" at the
	// same caret column.
	var collapsedRow string
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "collapsed note") {
			collapsedRow = line
			break
		}
	}
	if collapsedRow == "" {
		t.Fatalf("collapsed note message row missing.\noutput:\n%s", got)
	}
	if strings.Contains(collapsedRow, "^") {
		t.Errorf("collapsed row should not carry carets: %q", collapsedRow)
	}
	pipe := strings.Index(collapsedRow, "|")
	rest := collapsedRow[pipe+1:]
	msgStart := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] != ' ' {
			msgStart = i
			break
		}
	}
	if msgStart != caretCol {
		t.Errorf("collapsed message misaligned under tab-expanded caret: msg@%d caret@%d row=%q",
			msgStart, caretCol, collapsedRow)
	}
}
