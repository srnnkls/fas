package diag

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
)

// scoreKindMatch mirrors evaluator.ScoreKindMatch as the text renderer's
// no-close-arm threshold. Kept as an unexported const to avoid an evaluator
// import; pinned by tests against the real evaluator constant.
const scoreKindMatch = 100

// kindTextNames mirrors the JSON kind names so terminal output shows the
// same canonical token as structured output — one source of truth across
// --format=text|json|sarif.
var kindTextNames = map[cue.Kind]string{
	cue.NullKind:   "null",
	cue.BoolKind:   "bool",
	cue.IntKind:    "int",
	cue.FloatKind:  "float",
	cue.NumberKind: "number",
	cue.StringKind: "string",
	cue.BytesKind:  "bytes",
	cue.ListKind:   "list",
	cue.StructKind: "struct",
}

func kindText(k cue.Kind) string {
	if s, ok := kindTextNames[k]; ok {
		return s
	}
	return k.String()
}

// reasonRender carries the per-Reason text-layer output: the message that sits
// on the caret row, optional secondary frames that render as extra
// source-like snippets with their own carets (e.g. regex input echo or
// per-arm disjunction frames), and footer lines prepended to the diagnostic
// footer (e.g. "= hint:", "= note:").
type reasonRender struct {
	msg             string
	secondaryFrames []frameData
	footers         []string
	// helpOverride replaces the diagnostic's Help when non-nil. Used by
	// KeyMissing's empty-parent case to swap the legacy "has keys: " footer
	// for the more informative empty-struct phrasing.
	helpOverride *string
	// suppressExtraLabels, when true, signals the caller to skip any Note
	// Labels that would otherwise render per-arm caret frames (used by
	// DisjunctionFailed's no-close-arm case — the data layer still carries
	// the arms for JSON/SARIF, but text suppresses them).
	suppressExtraLabels bool
}

// renderReasonText translates a single Reason into text-layer output. Returns
// a zero reasonRender for an unknown variant (NF3 safety) — never panics.
func renderReasonText(r Reason, labelMsg string) reasonRender {
	switch v := r.(type) {
	case KindMismatch:
		return reasonRender{
			msg: fmt.Sprintf("want: %s, got: %s", kindText(v.Want), v.Actual),
		}
	case BoundViolation:
		msg := fmt.Sprintf("%s violates %s %s", v.Actual, v.Op, v.Bound)
		if v.Distance != "" {
			msg += " (" + v.Distance + ")"
		}
		return reasonRender{msg: msg}
	case RegexMismatch:
		return renderRegexMismatch(v)
	case ConjunctFailed:
		if v.Sub != nil {
			return renderReasonText(v.Sub, labelMsg)
		}
		return reasonRender{msg: v.Expr}
	case DisjunctionFailed:
		return renderDisjunctionFailed(v, labelMsg)
	case KeyMissing:
		return renderKeyMissing(v)
	case Provenance:
		return renderProvenance(v)
	}
	return reasonRender{}
}

// renderRegexMismatch builds the primary "got:" message plus a synthetic
// echo frame that underlines the divergence byte. DivergeAt < 0 drops the
// echo and offset footer (the renderer has no cut point to mark).
func renderRegexMismatch(v RegexMismatch) reasonRender {
	out := reasonRender{msg: fmt.Sprintf("got: %q", v.Input)}
	if v.DivergeAt < 0 {
		return out
	}

	expanded, visualDiverge := expandTabs(v.Input, v.DivergeAt+1)
	// expandTabs returns a 1-based visual column; the echo frame is
	// synthetic and has no real "col", so we bake the divergence column
	// directly and pass a plain expanded line below.

	trimmed, trimmedCol := trimRegexEcho(expanded, visualDiverge, regexEchoBudget)

	out.secondaryFrames = []frameData{{
		// LineNum = 0 signals "no gutter number" to the template — the
		// echo is pseudo-source, not a real snippet.
		LineNum:  0,
		Line:     trimmed,
		CaretCol: trimmedCol,
		Carets:   "^",
		Msg:      "",
	}}
	out.footers = []string{fmt.Sprintf("= note: regex first diverged at offset %d", v.DivergeAt)}
	return out
}

// regexEchoBudget is the target width of the echo frame in visual columns.
// 60 chars total with >=20 chars of context on each side keeps the marker
// centred while still fitting a narrow terminal.
const regexEchoBudget = 60

// trimRegexEcho crops expanded so the caret at visualCol (1-based) stays
// visible within budget visual columns. Trimmed edges render as a single
// `…` rune; the caret column compensates for any prefix trim so the marker
// still lands under the target byte.
func trimRegexEcho(expanded string, visualCol, budget int) (trimmed string, caretCol int) {
	// Fast path: fits.
	if len(expanded) <= budget {
		return expanded, visualCol
	}

	halfContext := budget / 2
	minContext := 20

	// Try to keep at least minContext chars on each side of visualCol.
	start := max(visualCol-halfContext, 0)
	// Shrink start if we're past the last minContext window.
	if len(expanded)-start < budget {
		start = max(len(expanded)-budget, 0)
	}
	end := min(start+budget, len(expanded))

	// Avoid trimming when the window covers the whole string.
	if start == 0 && end == len(expanded) {
		return expanded, visualCol
	}

	// Ensure minContext on the near side of the caret if possible.
	if visualCol-1-start < minContext && start > 0 {
		start = max(0, visualCol-1-minContext)
		end = min(len(expanded), start+budget)
	}

	prefix := ""
	suffix := ""
	shift := 0
	if start > 0 {
		prefix = "…"
		shift = start - 1 // caret col shifts left by start, +1 for the ellipsis.
	}
	if end < len(expanded) {
		suffix = "…"
	}
	return prefix + expanded[start:end] + suffix, visualCol - shift
}

// renderDisjunctionFailed produces the primary "closest arm was X" or flat
// "no arm was close" summary plus per-arm frames or an arms-footer note.
func renderDisjunctionFailed(v DisjunctionFailed, labelMsg string) reasonRender {
	actualPrefix := extractActualPrefix(labelMsg)

	if len(v.Arms) == 0 || v.Arms[0].Score < scoreKindMatch {
		// No-close-arm case: flat summary, arms footer, suppress ranked frames.
		msg := "no arm was close"
		if actualPrefix != "" {
			msg = actualPrefix + " — " + msg
		}
		armNames := make([]string, len(v.Arms))
		for i, a := range v.Arms {
			armNames[i] = a.Arm
		}
		footers := []string{"= note: tried arms: " + strings.Join(armNames, ", ")}
		return reasonRender{
			msg:                 msg,
			footers:             footers,
			suppressExtraLabels: true,
		}
	}

	// Happy path: name the closest arm.
	primary := "closest arm was " + v.Arms[0].Arm
	if actualPrefix != "" {
		primary = actualPrefix + " — " + primary
	}

	// Per-arm frames: each arm's Span seeds a synthetic secondary frame
	// whose caret sits at the arm's column. Rendered in arm order (already
	// sorted by Score desc in rankArms).
	frames := make([]frameData, 0, len(v.Arms))
	for _, a := range v.Arms {
		if a.Span.Line <= 0 {
			continue
		}
		frames = append(frames, frameData{
			File:    a.Span.File,
			LineNum: a.Span.Line,
			Col:     a.Span.Col,
			Len:     a.Span.Length,
			Msg:     a.Arm,
			FromArm: true,
		})
	}

	return reasonRender{
		msg:             primary,
		secondaryFrames: frames,
	}
}

// extractActualPrefix pulls a "got <actual>" substring out of the Label's
// Msg. Handles the two shapes currently emitted by localize: the legacy
// "no arm subsumes X" produced by disjunctionDiagnostic, and a plain
// "got X" already in the right shape. Empty Msg yields empty prefix.
func extractActualPrefix(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	const legacy = "no arm subsumes "
	if after, ok := strings.CutPrefix(msg, legacy); ok {
		return "got " + after
	}
	return msg
}

// renderKeyMissing dispatches between the "did you mean?" hint and the
// empty-parent help override.
func renderKeyMissing(v KeyMissing) reasonRender {
	out := reasonRender{}
	if len(v.AvailableKeys) == 0 {
		// Empty parent: replace the "has keys: " help with the empty-
		// struct phrasing. path is derived from Key's context — we only
		// know the key name here, so we rely on the Help string the
		// localize layer already populated to convey the parent path.
		// Localize sets Help = "<path> has keys: " with an empty list,
		// so we trim that to recover <path>.
		// The override text assembles at wireHelp in buildRenderData
		// where it has access to the existing Help; we just signal the
		// switch here by returning an empty string override.
		empty := ""
		out.helpOverride = &empty
		return out
	}
	if v.Suggestion != "" {
		out.footers = []string{fmt.Sprintf("= hint: did you mean %q?", v.Suggestion)}
	}
	return out
}

// renderProvenance emits the cross-file origin footer. Invalid spans drop
// silently (NF3).
func renderProvenance(v Provenance) reasonRender {
	if v.Span.File == "" || v.Span.Line <= 0 {
		return reasonRender{}
	}
	return reasonRender{
		footers: []string{fmt.Sprintf("= note: constraint introduced at %s:%d:%d",
			v.Span.File, v.Span.Line, v.Span.Col)},
	}
}

// parseEmptyParentPath peels the "<path> has keys: " shape back to <path>
// so the override footer reads "parent at <path> is an empty struct".
// Unknown shapes fall back to "<unknown>".
func parseEmptyParentPath(help string) string {
	const mid = " has keys: "
	before, _, ok := strings.Cut(help, mid)
	if !ok {
		return "<unknown>"
	}
	return before
}
