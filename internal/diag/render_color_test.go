package diag_test

import (
	"regexp"
	"strings"
	"testing"

	"cuelang.org/go/cue/token"

	"github.com/srnnkls/fas/internal/diag"
)

// ansiEscapeRE matches any ANSI CSI SGR escape sequence of the form
// `ESC [ <digits> m`. Used to assert presence / absence of color codes.
var ansiEscapeRE = regexp.MustCompile(`\x1b\[\d+m`)

// newColorDiag builds a minimal but fully-shaped Diagnostic + SourceCache
// for color tests so each test body focuses on palette behavior, not setup.
func newColorDiag(t *testing.T) (diag.Diagnostic, diag.SourceCache) {
	t.Helper()
	pos := newPos(t, "r.cue", 0)
	src := fakeSource{entries: map[token.Pos]fakeEntry{
		pos: {line: "x: 1", lineNum: 1, col: 1},
	}}
	d := diag.Diagnostic{
		Code:     "E0201",
		Severity: diag.SeverityError,
		Title:    "t",
		Primary:  diag.Label{Pos: pos, Len: 1, Msg: "m"},
		Help:     "try adding the key",
	}
	return d, src
}

// TestRenderWithPalette_NoColorPaletteIsIdentity: NoColorPalette must not add
// any bytes to severity/caret/location/hint strings, and the rendered output
// must contain zero ANSI escape sequences.
func TestRenderWithPalette_NoColorPaletteIsIdentity(t *testing.T) {
	d, src := newColorDiag(t)

	p := diag.NoColorPalette{}
	if got := p.Severity("error"); got != "error" {
		t.Errorf("NoColorPalette.Severity should be identity, got %q", got)
	}
	if got := p.Caret("^^^"); got != "^^^" {
		t.Errorf("NoColorPalette.Caret should be identity, got %q", got)
	}
	if got := p.Location("a.cue:1:1"); got != "a.cue:1:1" {
		t.Errorf("NoColorPalette.Location should be identity, got %q", got)
	}
	if got := p.Hint("did you mean"); got != "did you mean" {
		t.Errorf("NoColorPalette.Hint should be identity, got %q", got)
	}

	out := diag.RenderWithPalette(d, src, diag.NoColorPalette{})
	if ansiEscapeRE.MatchString(out) {
		t.Errorf("NoColorPalette must emit zero ANSI escapes.\noutput:\n%s", out)
	}
}

// TestRenderWithPalette_ANSIPaletteWrapsSeverity: the severity word carries
// the red SGR escape pair when rendered with ANSIPalette.
func TestRenderWithPalette_ANSIPaletteWrapsSeverity(t *testing.T) {
	d, src := newColorDiag(t)

	out := diag.RenderWithPalette(d, src, diag.ANSIPalette{})

	// Red escape (31) precedes the "error" word, reset (0) follows.
	if !strings.Contains(out, "\x1b[31merror\x1b[0m") {
		t.Errorf("severity `error` must be wrapped by red ANSI codes.\noutput:\n%s", out)
	}
}

// TestRenderWithPalette_ANSIPaletteWrapsWarningYellow: severity=warning maps
// to yellow (33), not red.
func TestRenderWithPalette_ANSIPaletteWrapsWarningYellow(t *testing.T) {
	d, src := newColorDiag(t)
	d.Severity = diag.SeverityWarning

	out := diag.RenderWithPalette(d, src, diag.ANSIPalette{})

	if !strings.Contains(out, "\x1b[33mwarning\x1b[0m") {
		t.Errorf("severity `warning` must be wrapped by yellow ANSI codes.\noutput:\n%s", out)
	}
}

// TestRenderWithPalette_ANSIPaletteWrapsNoteCyan: severity=note maps to cyan (36).
func TestRenderWithPalette_ANSIPaletteWrapsNoteCyan(t *testing.T) {
	d, src := newColorDiag(t)
	d.Severity = diag.SeverityNote

	out := diag.RenderWithPalette(d, src, diag.ANSIPalette{})

	if !strings.Contains(out, "\x1b[36mnote\x1b[0m") {
		t.Errorf("severity `note` must be wrapped by cyan ANSI codes.\noutput:\n%s", out)
	}
}

// TestRenderWithPalette_ANSIPaletteWrapsCaret: the caret run on the caret line
// is wrapped in red ANSI codes.
func TestRenderWithPalette_ANSIPaletteWrapsCaret(t *testing.T) {
	d, src := newColorDiag(t)

	out := diag.RenderWithPalette(d, src, diag.ANSIPalette{})

	// Caret sequence should be colored red (31) like severity.
	if !strings.Contains(out, "\x1b[31m^\x1b[0m") {
		t.Errorf("caret run must be wrapped by red ANSI codes.\noutput:\n%s", out)
	}
}

// TestRenderWithPalette_ANSIPaletteDimsLocation: the `--> file:line:col`
// location segment is wrapped in a dim (2) ANSI code.
func TestRenderWithPalette_ANSIPaletteDimsLocation(t *testing.T) {
	d, src := newColorDiag(t)

	out := diag.RenderWithPalette(d, src, diag.ANSIPalette{})

	if !strings.Contains(out, "\x1b[2mr.cue:1:1\x1b[0m") {
		t.Errorf("location `file:line:col` must be wrapped by dim ANSI codes.\noutput:\n%s", out)
	}
}

// TestRenderWithPalette_HintWrappedBlue: Hint() wraps input with blue (34).
// Independent of render pipeline so unit-tests the palette contract directly.
func TestRenderWithPalette_HintWrappedBlue(t *testing.T) {
	p := diag.ANSIPalette{}
	got := p.Hint("did you mean")
	want := "\x1b[34mdid you mean\x1b[0m"
	if got != want {
		t.Errorf("Hint wrapping mismatch.\n got: %q\nwant: %q", got, want)
	}
}

// TestRender_BackwardsCompatNoColor: the legacy `Render(d, src)` entry point
// continues to produce uncolored output for existing callers.
func TestRender_BackwardsCompatNoColor(t *testing.T) {
	d, src := newColorDiag(t)

	out := diag.Render(d, src)

	if ansiEscapeRE.MatchString(out) {
		t.Errorf("Render(d, src) must remain colorless by default.\noutput:\n%s", out)
	}
}

// TestResolveColorMode covers the selection matrix documented in F12:
// flag > env > TTY detection, with `auto` as the only mode that consults TTY.
func TestResolveColorMode(t *testing.T) {
	cases := []struct {
		name       string
		flag       string
		isTTY      bool
		noColorEnv bool
		wantANSI   bool
	}{
		{"always_tty", "always", true, false, true},
		{"always_non_tty", "always", false, false, true},
		{"always_no_color_env", "always", true, true, true},
		{"never_tty", "never", true, false, false},
		{"never_non_tty", "never", false, false, false},
		{"never_no_color_env", "never", false, true, false},
		{"auto_tty_no_env", "auto", true, false, true},
		{"auto_non_tty", "auto", false, false, false},
		{"auto_tty_no_color_env", "auto", true, true, false},
		{"empty_defaults_to_auto_tty", "", true, false, true},
		{"empty_defaults_to_auto_non_tty", "", false, false, false},
		{"unknown_defaults_to_auto_tty", "rainbow", true, false, true},
		{"unknown_defaults_to_auto_non_tty", "rainbow", false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := diag.ResolveColorMode(tc.flag, tc.isTTY, tc.noColorEnv)
			_, gotANSI := p.(diag.ANSIPalette)
			if gotANSI != tc.wantANSI {
				t.Errorf("ResolveColorMode(%q, isTTY=%v, noColor=%v) palette=%T, wantANSI=%v",
					tc.flag, tc.isTTY, tc.noColorEnv, p, tc.wantANSI)
			}
		})
	}
}

// TestPaletteInterface_Satisfaction: compile-time check that both palette
// structs satisfy the Palette interface. Keeps the surface locked — any future
// method addition forces both implementations to update.
func TestPaletteInterface_Satisfaction(t *testing.T) {
	var _ diag.Palette = diag.NoColorPalette{}
	var _ diag.Palette = diag.ANSIPalette{}
}
