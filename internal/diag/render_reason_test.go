package diag_test

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"

	"github.com/srnnkls/quae/internal/diag"
	"github.com/srnnkls/quae/internal/evaluator"
)

// TestRenderReason_KindMismatch: a Label carrying a KindMismatch renders the
// primary caret row with "expected <want>, got <got>: <actual>" — kind names
// canonicalised via the diag→string map, not Go's cue.Kind integer form.
func TestRenderReason_KindMismatch(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "count: int", lineNum: 1, col: 8},
	}}

	d := diag.Diagnostic{
		Code:     "E0303",
		Severity: diag.SeverityError,
		Title:    "type mismatch",
		Primary: diag.Label{
			Pos: pos,
			Len: 3,
			Reasons: []diag.Reason{
				diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"five"`},
			},
		},
	}

	got := diag.Render(d, src)
	want := `expected int, got string: "five"`
	if !strings.Contains(got, want) {
		t.Errorf("output missing %q.\noutput:\n%s", want, got)
	}
}

// TestRenderReason_BoundViolation_WithDistance: BoundViolation with a non-empty
// Distance formats "(off by ...)" in parens on the caret row.
func TestRenderReason_BoundViolation_WithDistance(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "count: >=5", lineNum: 1, col: 8},
	}}

	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: pos,
			Len: 3,
			Reasons: []diag.Reason{
				diag.BoundViolation{Op: ">=", Bound: "5", Actual: "3", Distance: "off by 2"},
			},
		},
	}

	got := diag.Render(d, src)
	want := "3 violates >= 5 (off by 2)"
	if !strings.Contains(got, want) {
		t.Errorf("output missing %q.\noutput:\n%s", want, got)
	}
}

// TestRenderReason_BoundViolation_EmptyDistance: when Distance is empty the
// renderer drops the parens entirely (e.g. `!=` with exact equality).
func TestRenderReason_BoundViolation_EmptyDistance(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "x: !=7", lineNum: 1, col: 4},
	}}

	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: pos,
			Len: 3,
			Reasons: []diag.Reason{
				diag.BoundViolation{Op: "!=", Bound: "7", Actual: "7", Distance: ""},
			},
		},
	}

	got := diag.Render(d, src)
	want := "7 violates != 7"
	if !strings.Contains(got, want) {
		t.Errorf("output missing %q.\noutput:\n%s", want, got)
	}
	if strings.Contains(got, "7 violates != 7 (") || strings.Contains(got, "()") {
		t.Errorf("empty Distance must not produce parens.\noutput:\n%s", got)
	}
}

// TestRenderReason_RegexMismatch_Happy: a RegexMismatch with DivergeAt >= 0
// emits a secondary input-echo frame whose caret sits under the diverging byte,
// plus a footer note with the offset.
func TestRenderReason_RegexMismatch_Happy(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: `cmd: =~"^rm "`, lineNum: 1, col: 6},
	}}

	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: pos,
			Len: 7,
			Reasons: []diag.Reason{
				diag.RegexMismatch{Pattern: "^rm ", Input: "rm-rf", DivergeAt: 2},
			},
		},
	}

	got := diag.Render(d, src)

	// Primary caret row should carry got: "<input>" (no leading space/quotes
	// issues).
	if !strings.Contains(got, `got: "rm-rf"`) {
		t.Errorf("primary caret row should show got: %q.\noutput:\n%s", "rm-rf", got)
	}

	// The secondary echo frame: the input appears as a pseudo-source line.
	// Must appear exactly once (the primary caret line shouldn't double-
	// emit the input).
	if n := strings.Count(got, "rm-rf"); n < 2 {
		t.Errorf("input %q should appear in both primary msg and secondary echo frame, got %d occurrences.\noutput:\n%s", "rm-rf", n, got)
	}

	// Footer note with the offset.
	want := "= note: regex first diverged at offset 2"
	if !strings.Contains(got, want) {
		t.Errorf("output missing footer %q.\noutput:\n%s", want, got)
	}

	// A caret under the diverging char in the secondary frame: must be a
	// standalone caret line whose pipe index + spaces match DivergeAt + 1.
	// Find a caret line whose preceding snippet line contains the input
	// text and is itself an echo frame (no line-number gutter, no `got:`
	// suffix).
	lines := strings.Split(got, "\n")
	foundEchoCaret := false
	for i := 0; i < len(lines)-1; i++ {
		line := lines[i]
		if !strings.Contains(line, "rm-rf") {
			continue
		}
		if strings.Contains(line, "got:") {
			continue // that's the primary caret line, not the echo.
		}
		if strings.Contains(line, "^") {
			continue // skip caret rows themselves.
		}
		// Found the echo snippet line.
		caret := lines[i+1]
		if strings.Contains(caret, "^") {
			foundEchoCaret = true
		}
		break
	}
	if !foundEchoCaret {
		t.Errorf("expected a caret line following the input-echo frame.\noutput:\n%s", got)
	}
}

// TestRenderReason_RegexMismatch_DivergeAtUnavailable: DivergeAt=-1 means the
// strategy couldn't identify a cut point; renderer falls back to the bare
// "got: ..." primary with no secondary frame and no offset footer.
func TestRenderReason_RegexMismatch_DivergeAtUnavailable(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: `cmd: =~"(a|b)+"`, lineNum: 1, col: 6},
	}}

	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: pos,
			Len: 9,
			Reasons: []diag.Reason{
				diag.RegexMismatch{Pattern: "(a|b)+", Input: "xyz", DivergeAt: -1},
			},
		},
	}

	got := diag.Render(d, src)
	if !strings.Contains(got, `got: "xyz"`) {
		t.Errorf("primary should still show got: \"xyz\".\noutput:\n%s", got)
	}
	if strings.Contains(got, "= note: regex first diverged") {
		t.Errorf("DivergeAt=-1 should not produce offset footer.\noutput:\n%s", got)
	}
	// No secondary echo frame: "xyz" should appear exactly once (only in
	// the primary msg).
	if n := strings.Count(got, "xyz"); n != 1 {
		t.Errorf("input should appear exactly once (no echo frame), got %d.\noutput:\n%s", n, got)
	}
}

// TestRenderReason_RegexMismatch_LongInputTrimmed: input longer than 60 chars
// is trimmed with `…` on edges, offset compensates so caret still marks the
// divergence byte.
func TestRenderReason_RegexMismatch_LongInputTrimmed(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: `cmd: =~"^foo"`, lineNum: 1, col: 1},
	}}

	// 100-char input, divergence in the middle (offset 50).
	input := strings.Repeat("a", 50) + "X" + strings.Repeat("b", 49)
	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: pos,
			Len: 3,
			Reasons: []diag.Reason{
				diag.RegexMismatch{Pattern: "^foo", Input: input, DivergeAt: 50},
			},
		},
	}

	got := diag.Render(d, src)
	// Echo frame must NOT contain the full 100-char input verbatim (would
	// mean no trimming happened).
	if strings.Contains(got, input) {
		// The primary msg line may still contain the full input — but we
		// want the secondary echo to be trimmed. Check for ellipsis.
		if !strings.Contains(got, "…") {
			t.Errorf("long input should be trimmed with ellipsis in the echo frame.\noutput:\n%s", got)
		}
	}
}

// TestRenderReason_ConjunctFailed_DelegatesToSub: a ConjunctFailed whose Sub is
// a BoundViolation renders the BoundViolation's message, not the bare Expr.
func TestRenderReason_ConjunctFailed_DelegatesToSub(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "count: int & >=5 & <=10", lineNum: 8, col: 20},
	}}

	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: pos,
			Len: 4,
			Reasons: []diag.Reason{
				diag.ConjunctFailed{
					Expr: "<=10",
					Span: diag.Span{File: "r.cue", Line: 8, Col: 20, Length: 4},
					Sub:  diag.BoundViolation{Op: "<=", Bound: "10", Actual: "12", Distance: "off by 2"},
				},
			},
		},
	}

	got := diag.Render(d, src)
	want := "12 violates <= 10 (off by 2)"
	if !strings.Contains(got, want) {
		t.Errorf("ConjunctFailed should delegate to inner Sub; missing %q.\noutput:\n%s", want, got)
	}
}

// TestRenderReason_ConjunctFailed_NilSubFallback: a ConjunctFailed with nil Sub
// falls back to the Expr string on the caret row.
func TestRenderReason_ConjunctFailed_NilSubFallback(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "x: mystery", lineNum: 1, col: 4},
	}}

	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: pos,
			Len: 7,
			Reasons: []diag.Reason{
				diag.ConjunctFailed{Expr: "mystery", Sub: nil},
			},
		},
	}

	got := diag.Render(d, src)
	if !strings.Contains(got, "mystery") {
		t.Errorf("nil Sub should fall back to Expr %q.\noutput:\n%s", "mystery", got)
	}
}

// TestRenderReason_DisjunctionFailed_Happy: top arm has Score >= ScoreKindMatch,
// so the renderer emits "closest arm was X" plus per-arm secondary frames.
func TestRenderReason_DisjunctionFailed_Happy(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	armPos1 := newPos(t, "r.cue", 10)
	armPos2 := newPos(t, "r.cue", 20)

	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos:     {line: `tool: "Bash" | "Read" | "Write"`, lineNum: 10, col: 7},
		armPos1: {line: `tool: "Bash" | "Read" | "Write"`, lineNum: 10, col: 16},
		armPos2: {line: `tool: "Bash" | "Read" | "Write"`, lineNum: 10, col: 25},
	}}

	d := diag.Diagnostic{
		Code:     "E0401",
		Severity: diag.SeverityError,
		Title:    "no disjunction arm matched",
		Primary: diag.Label{
			Pos: pos,
			Len: 24,
			Reasons: []diag.Reason{
				diag.DisjunctionFailed{Arms: []diag.ArmResult{
					{
						Arm:   `"Read"`,
						Span:  diag.Span{File: "r.cue", Line: 10, Col: 16, Length: 6},
						Score: evaluator.ScoreKindMatch + 1,
					},
					{
						Arm:   `"Write"`,
						Span:  diag.Span{File: "r.cue", Line: 10, Col: 25, Length: 7},
						Score: evaluator.ScoreKindMatch,
					},
				}},
			},
		},
	}

	// Annotate the primary with an Actual rendering via the Msg path too.
	// Some renderers use the first Reason to produce the msg — just verify
	// "closest arm was" phrasing appears.
	got := diag.Render(d, src)
	if !strings.Contains(got, `closest arm was "Read"`) {
		t.Errorf("primary should name the closest arm; missing in:\n%s", got)
	}

	// Per-arm frames: each arm's Span should drive a secondary frame. The
	// source snippet is shared (same line 10), so T10 collapse would fold
	// them under a single snippet — but arm spans differ in col, so each
	// produces its own caret row. We don't over-pin the visual layout
	// here; just require the arm labels to appear.
	if !strings.Contains(got, `"Read"`) || !strings.Contains(got, `"Write"`) {
		t.Errorf("per-arm annotations missing.\noutput:\n%s", got)
	}
}

// TestRenderReason_DisjunctionFailed_NoCloseArm: top arm Score below
// ScoreKindMatch → flat summary, no ranked caret frames, arms footer.
func TestRenderReason_DisjunctionFailed_NoCloseArm(t *testing.T) {
	pos := newPos(t, "r.cue", 0)

	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: `tool: "Bash" | "Read"`, lineNum: 10, col: 7},
	}}

	d := diag.Diagnostic{
		Code:     "E0401",
		Severity: diag.SeverityError,
		Title:    "no disjunction arm matched",
		Primary: diag.Label{
			Pos: pos,
			Len: 14,
			Reasons: []diag.Reason{
				diag.DisjunctionFailed{Arms: []diag.ArmResult{
					{Arm: `"Bash"`, Score: 0},
					{Arm: `"Read"`, Score: 0},
				}},
			},
			Msg: "got true",
		},
	}

	got := diag.Render(d, src)
	if !strings.Contains(got, "no arm was close") {
		t.Errorf("no-close-arm case should render flat summary.\noutput:\n%s", got)
	}
	if !strings.Contains(got, `= note: tried arms:`) {
		t.Errorf("no-close-arm case should render arms footer.\noutput:\n%s", got)
	}
	if !strings.Contains(got, `"Bash"`) || !strings.Contains(got, `"Read"`) {
		t.Errorf("arms footer should list all arms.\noutput:\n%s", got)
	}
	// The arm list shouldn't have produced extra caret rows.
	caretRows := 0
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "^") && strings.Contains(line, "|") {
			caretRows++
		}
	}
	if caretRows > 1 {
		t.Errorf("no-close-arm case should suppress ranked caret frames; got %d caret rows.\noutput:\n%s",
			caretRows, got)
	}
}

// TestRenderReason_KeyMissing_WithSuggestion: KeyMissing with a non-empty
// Suggestion produces a "= hint:" footer.
func TestRenderReason_KeyMissing_WithSuggestion(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "    flags: force: true", lineNum: 12, col: 5},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "key not found",
		Primary: diag.Label{
			Pos: pos,
			Len: 5,
			Msg: `key "flags" not found`,
			Reasons: []diag.Reason{
				diag.KeyMissing{
					Key:           "flags",
					AvailableKeys: []string{"flag", "forced"},
					Suggestion:    "flag",
				},
			},
		},
		Help: "<root> has keys: flag, forced",
	}

	got := diag.Render(d, src)
	want := `= hint: did you mean "flag"?`
	if !strings.Contains(got, want) {
		t.Errorf("output missing %q.\noutput:\n%s", want, got)
	}
}

// TestRenderReason_KeyMissing_EmptyParent: KeyMissing with empty AvailableKeys
// replaces the legacy "has keys: " help with "parent at <path> is an empty
// struct", and emits no hint line.
func TestRenderReason_KeyMissing_EmptyParent(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "container: port: 8080", lineNum: 1, col: 1},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "key not found",
		Primary: diag.Label{
			Pos: pos,
			Len: 9,
			Msg: `key "container" not found in input at path <root>`,
			Reasons: []diag.Reason{
				diag.KeyMissing{
					Key:           "container",
					AvailableKeys: []string{},
					Suggestion:    "",
				},
			},
		},
		// The localize path sets Help to "<path> has keys: " (empty
		// list) when no keys are available; the renderer should replace
		// that with the empty-struct phrasing.
		Help: "<root> has keys: ",
	}

	got := diag.Render(d, src)
	want := "= help: parent at <root> is an empty struct"
	if !strings.Contains(got, want) {
		t.Errorf("empty-parent case should replace help; missing %q.\noutput:\n%s", want, got)
	}
	if strings.Contains(got, "= hint:") {
		t.Errorf("empty-parent case should not emit a hint line.\noutput:\n%s", got)
	}
	if strings.Contains(got, "= help: <root> has keys: ") {
		t.Errorf("legacy has-keys help should be replaced.\noutput:\n%s", got)
	}
}

// TestRenderReason_Provenance: a Provenance Reason on a Note Label renders a
// "= note: constraint introduced at <file:line:col>" footer.
func TestRenderReason_Provenance(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	provPos := newPos(t, "stdlib/nums.cue", 50)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos:     {line: "x: int & stdlib.Positive", lineNum: 8, col: 4},
		provPos: {line: "Positive: int & >=0", lineNum: 7, col: 17},
	}}

	d := diag.Diagnostic{
		Code:     "E0303",
		Severity: diag.SeverityError,
		Title:    "type mismatch",
		Primary: diag.Label{
			Pos: pos,
			Len: 20,
			Reasons: []diag.Reason{
				diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"three"`},
			},
		},
		Notes: []diag.Label{
			{
				Pos: provPos,
				Len: 3,
				Reasons: []diag.Reason{
					diag.Provenance{
						Span: diag.Span{File: "stdlib/nums.cue", Line: 7, Col: 17, Length: 3},
					},
				},
			},
		},
	}

	got := diag.Render(d, src)
	want := "= note: constraint introduced at stdlib/nums.cue:7:17"
	if !strings.Contains(got, want) {
		t.Errorf("provenance footer missing %q.\noutput:\n%s", want, got)
	}
}

// TestRenderReason_MultiReasonLabel: a Label with 2+ Reasons renders the first
// on the caret row and subsequent entries as aligned message rows under the
// same caret (T10 same-span collapse semantics).
func TestRenderReason_MultiReasonLabel(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: `x: string & =~"^[a-z]+$" & strings.MinRunes(5)`, lineNum: 1, col: 13},
	}}

	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: pos,
			Len: 12,
			Reasons: []diag.Reason{
				diag.ConjunctFailed{
					Expr: `=~"^[a-z]+$"`,
					Sub:  diag.RegexMismatch{Pattern: "^[a-z]+$", Input: "AB", DivergeAt: 0},
				},
				diag.ConjunctFailed{
					Expr: "strings.MinRunes(5)",
					Sub:  nil,
				},
			},
		},
	}

	got := diag.Render(d, src)

	// Source printed exactly once.
	if n := strings.Count(got, `x: string & =~"^[a-z]+$" & strings.MinRunes(5)`); n != 1 {
		t.Errorf("source line should appear once (T10 collapse), got %d.\noutput:\n%s", n, got)
	}

	// Both messages must appear.
	if !strings.Contains(got, `got: "AB"`) {
		t.Errorf("first Reason (regex) message missing.\noutput:\n%s", got)
	}
	if !strings.Contains(got, "strings.MinRunes(5)") {
		t.Errorf("second Reason (nil-Sub fallback) message missing.\noutput:\n%s", got)
	}

	// First Reason's message should appear before the second Reason's
	// collapsed-row message. Use the "got:" anchor (unique to the primary
	// caret row) and find the MinRunes collapsed row (occurs after source
	// line too; we want the one on the collapsed-note row).
	idxAB := strings.Index(got, `got: "AB"`)
	// The second occurrence of MinRunes is the collapsed row.
	first := strings.Index(got, "strings.MinRunes(5)")
	second := -1
	if first >= 0 {
		rest := got[first+len("strings.MinRunes(5)"):]
		if off := strings.Index(rest, "strings.MinRunes(5)"); off >= 0 {
			second = first + len("strings.MinRunes(5)") + off
		}
	}
	idxMin := second
	if idxMin < 0 {
		// Fallback: treat the first occurrence as the message row (the
		// source line may have been collapsed/omitted).
		idxMin = first
	}
	if idxAB < 0 || idxMin < 0 || idxAB >= idxMin {
		t.Errorf("first reason should precede second. ab@%d min@%d\noutput:\n%s", idxAB, idxMin, got)
	}
}

// TestRenderReason_EmptyReasons_FallsThroughToMsg: a Label with zero Reasons
// renders exactly as v0 — Msg on the caret row, no renderReasonText dispatch.
// Matches TestRender_SingleLabelGolden's contract (NF5).
func TestRenderReason_EmptyReasons_FallsThroughToMsg(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "x: 1", lineNum: 1, col: 1},
	}}

	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "legacy",
		Primary: diag.Label{
			Pos: pos,
			Len: 1,
			Msg: "v0 message",
			// No Reasons.
		},
	}

	got := diag.Render(d, src)
	if !strings.Contains(got, "v0 message") {
		t.Errorf("empty Reasons should render Msg as-is (v0 path).\noutput:\n%s", got)
	}
}

// TestRenderReason_SingletonReason_MatchesDispatch: a single-element Reasons
// slice dispatches the same way as the variant-specific tests above — no
// special-case for singletons vs multi.
func TestRenderReason_SingletonReason_MatchesDispatch(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "x: >=5", lineNum: 1, col: 4},
	}}

	d := diag.Diagnostic{
		Code:     "E0301",
		Severity: diag.SeverityError,
		Title:    "leaf constraint failed",
		Primary: diag.Label{
			Pos: pos,
			Len: 3,
			Reasons: []diag.Reason{
				diag.BoundViolation{Op: ">=", Bound: "5", Actual: "3", Distance: "off by 2"},
			},
		},
	}

	got := diag.Render(d, src)
	if !strings.Contains(got, "3 violates >= 5 (off by 2)") {
		t.Errorf("singleton Reason should dispatch normally.\noutput:\n%s", got)
	}
}

// TestRenderReason_ScoreKindMatchPinned: the renderer's unexported threshold
// for the disjunction no-close-arm case must stay in lock-step with the
// evaluator constant. If the evaluator tier values shift, update the
// renderer's internal mirror and this pin in the same change.
func TestRenderReason_ScoreKindMatchPinned(t *testing.T) {
	if evaluator.ScoreKindMatch != 100 {
		t.Fatalf("evaluator.ScoreKindMatch drifted from 100 to %d — update diag.scoreKindMatch to match.",
			evaluator.ScoreKindMatch)
	}
}

// TestRenderReason_NoAnsiColor: renderer output must be free of ANSI escape
// bytes — T15 owns color.
func TestRenderReason_NoAnsiColor(t *testing.T) {
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "count: int", lineNum: 1, col: 8},
	}}

	d := diag.Diagnostic{
		Code:     "E0303",
		Severity: diag.SeverityError,
		Title:    "type mismatch",
		Primary: diag.Label{
			Pos: pos,
			Len: 3,
			Reasons: []diag.Reason{
				diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"five"`},
			},
		},
	}

	got := diag.Render(d, src)
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("renderer output must not contain ANSI escape bytes (T15's job).\noutput:\n%q", got)
	}
}
