package diag

// Palette wraps semantic diagnostic spans (severity, caret, location, hint)
// with presentation bytes. Implementations are pure functions over strings;
// the renderer stays free of environment probes. A CLI caller chooses a
// palette via ResolveColorMode and feeds it to RenderWithPalette.
type Palette interface {
	Severity(s string) string
	Caret(s string) string
	Location(s string) string
	Hint(s string) string
}

// NoColorPalette is the identity palette: every wrapper returns its input
// unchanged. Used when color is disabled and as the default for the legacy
// Render entry point so existing callers see byte-identical output.
type NoColorPalette struct{}

func (NoColorPalette) Severity(s string) string { return s }
func (NoColorPalette) Caret(s string) string    { return s }
func (NoColorPalette) Location(s string) string { return s }
func (NoColorPalette) Hint(s string) string     { return s }

// ANSIPalette applies SGR escapes per span class:
//
//	severity → red/yellow/cyan (matching the error/warning/note word),
//	caret    → red,
//	location → dim,
//	hint     → blue.
//
// The severity escape is chosen by inspecting the input word so a single
// palette instance handles all three severities without a second constructor.
type ANSIPalette struct{}

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
	ansiBlue   = "\x1b[34m"
	ansiDim    = "\x1b[2m"
)

func (ANSIPalette) Severity(s string) string {
	switch s {
	case "warning":
		return ansiYellow + s + ansiReset
	case "note":
		return ansiCyan + s + ansiReset
	default:
		return ansiRed + s + ansiReset
	}
}

func (ANSIPalette) Caret(s string) string    { return ansiRed + s + ansiReset }
func (ANSIPalette) Location(s string) string { return ansiDim + s + ansiReset }
func (ANSIPalette) Hint(s string) string     { return ansiBlue + s + ansiReset }

// ResolveColorMode picks a palette from the CLI flag value, whether stdout is
// a TTY, and whether NO_COLOR is set in the environment. Precedence:
// flag=always forces ANSI, flag=never forces NoColor, otherwise auto consults
// isTTY and noColorEnv. Empty or unknown flag values default to auto so a
// typo'd --color value never produces colored output on a pipe.
func ResolveColorMode(flag string, isTTY bool, noColorEnv bool) Palette {
	switch flag {
	case "always":
		return ANSIPalette{}
	case "never":
		return NoColorPalette{}
	}
	// auto (explicit or default): ANSI only if attached to a terminal and the
	// user has not opted out via the community NO_COLOR env.
	if isTTY && !noColorEnv {
		return ANSIPalette{}
	}
	return NoColorPalette{}
}
