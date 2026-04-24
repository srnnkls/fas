package diag

import (
	"slices"
	"strconv"
	"strings"
	"text/template"

	"cuelang.org/go/cue/token"
)

// SourceCache resolves a token.Pos to the source line it points at, with the
// 1-based line number and column. It returns ok=false when the position is
// unknown or the file is unavailable.
type SourceCache interface {
	LineAt(pos token.Pos) (line string, lineNum int, col int, ok bool)
}

// tabWidth is the number of spaces each tab expands to in rendered snippets.
// The caret's visual column is shifted by (tabsBefore * (tabWidth-1)) so the
// caret sits beneath the target byte after expansion.
const tabWidth = 4

// Render formats a Diagnostic into a multi-line, Rust-style string using src
// to fetch snippet lines. Missing source degrades to a "position unknown"
// marker; Render never panics on bad input per scope NF3.
func Render(d Diagnostic, src SourceCache) string {
	data := buildRenderData(d, src)

	var b strings.Builder
	if err := renderTmpl.Execute(&b, data); err != nil {
		// Template/data shape drift is a programmer bug, not a runtime
		// condition — but NF3 forbids panics. Degrade to a terse marker so
		// the caller still gets an identifiable line.
		return "error: render template failure: " + err.Error() + "\n"
	}
	return b.String()
}

type renderData struct {
	Severity string
	Code     string
	Title    string
	Location string
	Gutter   int
	Labels   []labelData
	Help     string
}

type labelData struct {
	LineNum  int
	Line     string
	CaretCol int
	Carets   string
	Msg      string
	// Collapsed is true when this label shares its (file, line, col, len)
	// tuple with a previously-rendered label. Collapsed labels emit only an
	// aligned message row under the caret column of the first occurrence;
	// no snippet, no caret row.
	Collapsed bool
}

type resolvedLabel struct {
	label   Label
	line    string
	lineNum int
	col     int
	ok      bool
}

func buildRenderData(d Diagnostic, src SourceCache) renderData {
	_, primaryLineNum, primaryCol, primaryOK := src.LineAt(d.Primary.Pos)

	resolved := orderedLabels(d, src)
	gutter := gutterWidth(resolved)

	labels := make([]labelData, 0, len(resolved))
	// Track previously-rendered spans by (file, line, col, len). A later
	// label matching any prior tuple renders as an aligned message row
	// only — skipping the snippet and caret to keep visual density low
	// when several messages attach to the same span (F7).
	type spanKey struct {
		file   string
		line   int
		col    int
		length int
	}
	seen := make(map[spanKey]int, len(resolved)) // value = visual caret column of first occurrence
	for _, r := range resolved {
		if !r.ok {
			continue
		}
		expanded, visualCol := expandTabs(r.line, r.col)
		key := spanKey{
			file:   r.label.Pos.Filename(),
			line:   r.lineNum,
			col:    r.col,
			length: r.label.Len,
		}
		if firstCol, ok := seen[key]; ok {
			labels = append(labels, labelData{
				CaretCol:  firstCol,
				Msg:       r.label.Msg,
				Collapsed: true,
			})
			continue
		}
		seen[key] = visualCol
		labels = append(labels, labelData{
			LineNum:  r.lineNum,
			Line:     expanded,
			CaretCol: visualCol,
			Carets:   carets(r.label.Len),
			Msg:      r.label.Msg,
		})
	}

	return renderData{
		Severity: severityWord(d.Severity),
		Code:     d.Code,
		Title:    d.Title,
		Location: locationLine(d.Primary, primaryLineNum, primaryCol, primaryOK),
		Gutter:   gutter,
		Labels:   labels,
		Help:     d.Help,
	}
}

func orderedLabels(d Diagnostic, src SourceCache) []resolvedLabel {
	out := make([]resolvedLabel, 0, 1+len(d.Notes))

	pl, pn, pc, pok := src.LineAt(d.Primary.Pos)
	out = append(out, resolvedLabel{
		label: d.Primary, line: pl, lineNum: pn, col: pc, ok: pok,
	})

	for _, n := range d.Notes {
		nl, nn, nc, nok := src.LineAt(n.Pos)
		out = append(out, resolvedLabel{
			label: n, line: nl, lineNum: nn, col: nc, ok: nok,
		})
	}

	slices.SortStableFunc(out, func(a, b resolvedLabel) int {
		return a.lineNum - b.lineNum
	})
	return out
}

func gutterWidth(labels []resolvedLabel) int {
	maxLine := 1
	for _, l := range labels {
		if l.ok && l.lineNum > maxLine {
			maxLine = l.lineNum
		}
	}
	return len(strconv.Itoa(maxLine))
}

// expandTabs replaces tabs in src with tabWidth spaces and returns the visual
// column corresponding to the 1-based byte column col. Tabs AT the target
// column do not count toward the shift (strict-before semantics); the column
// scan is clamped to len(src) so a col past end-of-line is safe.
func expandTabs(src string, col int) (expanded string, visualCol int) {
	limit := max(min(col-1, len(src)), 0)
	tabsBefore := strings.Count(src[:limit], "\t")
	expanded = strings.ReplaceAll(src, "\t", strings.Repeat(" ", tabWidth))
	return expanded, col + tabsBefore*(tabWidth-1)
}

func severityWord(s Severity) string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityNote:
		return "note"
	default:
		return "error"
	}
}

func locationLine(primary Label, lineNum, col int, ok bool) string {
	if !ok {
		return "  --> position unknown"
	}
	return "  --> " + primary.Pos.Filename() + ":" + strconv.Itoa(lineNum) + ":" + strconv.Itoa(col)
}

func carets(n int) string {
	if n <= 0 {
		return "^"
	}
	return strings.Repeat("^", n)
}

var renderTmpl = template.Must(template.New("diag").
	Funcs(template.FuncMap{
		"pad":    func(n int) string { return strings.Repeat(" ", n) },
		"lineNo": func(gutter, n int) string { return leftPad(strconv.Itoa(n), gutter) },
	}).
	Parse(renderTemplate))

func leftPad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

const renderTemplate = "" +
	"{{.Severity}}[{{.Code}}]: {{.Title}}\n" +
	"{{.Location}}\n" +
	"{{pad .Gutter}} |\n" +
	"{{range $i, $l := .Labels}}" +
	"{{if $l.Collapsed}}" +
	"{{pad $.Gutter}} |{{pad $l.CaretCol}}{{$l.Msg}}\n" +
	"{{else}}" +
	"{{if $i}}{{pad $.Gutter}} |\n{{end}}" +
	"{{lineNo $.Gutter $l.LineNum}} | {{$l.Line}}\n" +
	"{{pad $.Gutter}} |{{pad $l.CaretCol}}{{$l.Carets}}{{with $l.Msg}} {{.}}{{end}}\n" +
	"{{end}}" +
	"{{end}}" +
	"{{with .Help}}" +
	"{{pad $.Gutter}} |\n" +
	"{{pad $.Gutter}} = help: {{.}}\n" +
	"{{end}}"
