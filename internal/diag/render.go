package diag

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"cuelang.org/go/cue/token"
)

// SourceCache resolves a token.Pos to the source line it points at, with the
// 1-based line number and column. It returns ok=false when the position is
// unknown or the file is unavailable.
type SourceCache interface {
	LineAt(pos token.Pos) (line string, lineNum int, col int, ok bool)
}

// Render formats a Diagnostic into a multi-line, Rust-style string using src
// to fetch snippet lines. Missing source degrades to a "position unknown"
// marker; Render never panics on bad input per scope NF3.
func Render(d Diagnostic, src SourceCache) string {
	var b strings.Builder

	writeHeader(&b, d)

	_, primaryLineNum, primaryCol, primaryOK := src.LineAt(d.Primary.Pos)
	writeLocation(&b, d.Primary, primaryLineNum, primaryCol, primaryOK)

	labels := orderedLabels(d, src)
	gutter := gutterWidth(labels)

	writeBlankGutter(&b, gutter)
	for i, l := range labels {
		writeLabelBlock(&b, l, gutter)
		if i < len(labels)-1 {
			writeBlankGutter(&b, gutter)
		}
	}

	if d.Help != "" {
		writeBlankGutter(&b, gutter)
		writeHelp(&b, d.Help, gutter)
	}

	return b.String()
}

type resolvedLabel struct {
	label   Label
	line    string
	lineNum int
	col     int
	ok      bool
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

func writeHeader(b *strings.Builder, d Diagnostic) {
	b.WriteString(severityWord(d.Severity))
	b.WriteByte('[')
	b.WriteString(d.Code)
	b.WriteString("]: ")
	b.WriteString(d.Title)
	b.WriteByte('\n')
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

func writeLocation(b *strings.Builder, primary Label, lineNum, col int, ok bool) {
	if !ok {
		b.WriteString("  --> position unknown\n")
		return
	}
	file := primary.Pos.Filename()
	fmt.Fprintf(b, "  --> %s:%d:%d\n", file, lineNum, col)
}

func writeBlankGutter(b *strings.Builder, gutter int) {
	b.WriteString(strings.Repeat(" ", gutter))
	b.WriteString(" |\n")
}

func writeLabelBlock(b *strings.Builder, r resolvedLabel, gutter int) {
	if !r.ok {
		return
	}

	fmt.Fprintf(b, "%*d | %s\n", gutter, r.lineNum, r.line)

	b.WriteString(strings.Repeat(" ", gutter))
	b.WriteString(" |")
	b.WriteString(strings.Repeat(" ", r.col))
	b.WriteString(carets(r.label.Len))
	if r.label.Msg != "" {
		b.WriteByte(' ')
		b.WriteString(r.label.Msg)
	}
	b.WriteByte('\n')
}

func carets(n int) string {
	if n <= 0 {
		return "^"
	}
	return strings.Repeat("^", n)
}

func writeHelp(b *strings.Builder, help string, gutter int) {
	b.WriteString(strings.Repeat(" ", gutter))
	b.WriteString(" = help: ")
	b.WriteString(help)
	b.WriteByte('\n')
}
