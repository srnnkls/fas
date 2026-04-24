package diag

import (
	"encoding/json"
	"io"

	"cuelang.org/go/cue/token"
)

// diagnosticWire is the JSON envelope for a Diagnostic. Field order here
// pins the emission order of top-level keys and is part of the stable
// schema documented in json_schema.md.
type diagnosticWire struct {
	Code     string         `json:"code"`
	Severity string         `json:"severity"`
	Title    string         `json:"title"`
	Location locationWire   `json:"location"`
	Primary  labelOutWire   `json:"primary"`
	Notes    []labelOutWire `json:"notes,omitempty"`
	Help     string         `json:"help,omitempty"`
}

// locationWire mirrors the primary Label's origin at the top level so
// consumers can address a diagnostic by (file, line, col) without reaching
// into primary.pos.
type locationWire struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

// labelPosWire is the positional envelope carried on every output Label —
// a flat {line, col, len} triple so downstream consumers (LSP, SARIF
// extractors) don't need to cross-reference the top-level location for
// every note.
type labelPosWire struct {
	Line int `json:"line"`
	Col  int `json:"col"`
	Len  int `json:"len"`
}

// labelOutWire is the Diagnostic-level Label shape. It is intentionally
// flat (pos is a {line,col,len} triple) rather than reusing labelWire
// from reason_json.go, which carries a nested Span for standalone Label
// round-tripping. The two shapes target different use cases; keeping them
// separate lets the Reason-only round-trip (T1) and the Diagnostic
// round-trip (T13) evolve independently.
type labelOutWire struct {
	Pos     labelPosWire      `json:"pos"`
	Msg     string            `json:"msg"`
	Reasons []json.RawMessage `json:"reasons,omitempty"`
}

// severityString maps Severity to its JSON token. Kept distinct from
// render.go's severityWord so a future renaming on either side stays local.
func severityString(s Severity) string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityNote:
		return "note"
	default:
		return "error"
	}
}

// severityFromString inverts severityString. Unknown inputs default to
// SeverityError — a severity we can't classify shouldn't be quietly
// downgraded to a note.
func severityFromString(s string) Severity {
	switch s {
	case "warning":
		return SeverityWarning
	case "note":
		return SeverityNote
	default:
		return SeverityError
	}
}

// RenderJSON encodes d as a single JSON object followed by a trailing
// newline, ready for ND-JSON consumption. Schema documented in
// internal/diag/json_schema.md.
func RenderJSON(d Diagnostic) []byte {
	w := diagnosticWire{
		Code:     d.Code,
		Severity: severityString(d.Severity),
		Title:    d.Title,
		Location: locationFromLabel(d.Primary),
		Primary:  labelToWire(d.Primary),
	}
	if len(d.Notes) > 0 {
		w.Notes = make([]labelOutWire, len(d.Notes))
		for i, n := range d.Notes {
			w.Notes[i] = labelToWire(n)
		}
	}
	if d.Help != "" {
		w.Help = d.Help
	}
	body, err := json.Marshal(w)
	if err != nil {
		// NF3: never panic. Emit a degraded but syntactically-valid
		// object so downstream parsers still receive a parseable line.
		return []byte(`{"code":"` + d.Code + `","error":"render_json_failed"}` + "\n")
	}
	return append(body, '\n')
}

// RenderJSONStream writes one JSON object per diagnostic to w, each
// terminated by a newline. Canonical ND-JSON producer for `--format=json`.
func RenderJSONStream(w io.Writer, diags []Diagnostic) error {
	for i := range diags {
		if _, err := w.Write(RenderJSON(diags[i])); err != nil {
			return err
		}
	}
	return nil
}

// locationFromLabel lifts a Label's Pos to the top-level location shape.
// Invalid positions yield the zero locationWire.
func locationFromLabel(l Label) locationWire {
	p := l.Pos
	if !p.IsValid() {
		return locationWire{}
	}
	return locationWire{File: p.Filename(), Line: p.Line(), Col: p.Column()}
}

// labelToWire converts a Label to its Diagnostic-envelope form. Reason
// marshalling reuses each variant's MarshalJSON (T1), so the snake_case
// "type" tags stay consistent across the JSON and SARIF formats.
func labelToWire(l Label) labelOutWire {
	var line, col int
	if l.Pos.IsValid() {
		line = l.Pos.Line()
		col = l.Pos.Column()
	}
	out := labelOutWire{
		Pos: labelPosWire{Line: line, Col: col, Len: l.Len},
		Msg: l.Msg,
	}
	if len(l.Reasons) > 0 {
		out.Reasons = make([]json.RawMessage, len(l.Reasons))
		for i, r := range l.Reasons {
			raw, err := marshalReason(r)
			if err != nil {
				out.Reasons[i] = json.RawMessage("null")
				continue
			}
			out.Reasons[i] = raw
		}
	}
	return out
}

// UnmarshalJSON restores a Diagnostic from its RenderJSON output. Label
// positions are reconstructed inside a synthetic token.File so Line/Col
// survive the round-trip while remaining within the IsValid() envelope.
func (d *Diagnostic) UnmarshalJSON(data []byte) error {
	var w struct {
		Code     string         `json:"code"`
		Severity string         `json:"severity"`
		Title    string         `json:"title"`
		Location locationWire   `json:"location"`
		Primary  labelOutWire   `json:"primary"`
		Notes    []labelOutWire `json:"notes"`
		Help     string         `json:"help"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	d.Code = w.Code
	d.Severity = severityFromString(w.Severity)
	d.Title = w.Title
	d.Help = w.Help
	d.Primary = wireToLabel(w.Primary, w.Location.File)
	if len(w.Notes) > 0 {
		d.Notes = make([]Label, len(w.Notes))
		for i, n := range w.Notes {
			d.Notes[i] = wireToLabel(n, w.Location.File)
		}
	} else {
		d.Notes = nil
	}
	return nil
}

// wireToLabel inverts labelToWire. The original token.FileSet is gone by
// the time we decode, so we mint a fresh token.File large enough to hold
// Line-many synthetic lines and pick a position that reports back the same
// Line/Col. Round-trip equality then depends only on Line/Col agreeing,
// which is the invariant callers care about.
func wireToLabel(w labelOutWire, fallbackFile string) Label {
	l := Label{
		Len: w.Pos.Len,
		Msg: w.Msg,
		Pos: synthesizePos(fallbackFile, w.Pos.Line, w.Pos.Col),
	}
	if len(w.Reasons) > 0 {
		l.Reasons = make([]Reason, len(w.Reasons))
		for i, raw := range w.Reasons {
			r, err := UnmarshalReason(raw)
			if err != nil {
				l.Reasons[i] = nil
				continue
			}
			l.Reasons[i] = r
		}
	}
	return l
}

// synthesizePos builds a token.Pos that reports the given filename, line,
// and column. It allocates a dedicated token.File per call; for large
// note-heavy diagnostics a single shared file would be cheaper, but
// correctness here matters more than allocation churn on a round-trip
// that by design runs outside the evaluator hot path.
func synthesizePos(filename string, line, col int) token.Pos {
	if line <= 0 {
		return token.NoPos
	}
	// Each synthetic line gets a large stride so column offsets never
	// overlap across lines. 4096 is generous for column widths in real
	// source and keeps arithmetic straightforward.
	const stride = 4096
	size := stride * (line + 1)
	f := token.NewFile(filename, 0, size)
	for i := 1; i < line; i++ {
		f.AddLine(i * stride)
	}
	offset := (line-1)*stride + max(col-1, 0)
	return f.Pos(offset, token.NoRelPos)
}
