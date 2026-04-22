package diag

import "cuelang.org/go/cue/token"

// Severity classifies a Diagnostic as error, warning, or note.
type Severity int

// Severity levels for a Diagnostic, ordered from most to least severe.
const (
	SeverityError Severity = iota
	SeverityWarning
	SeverityNote
)

// Diagnostic is a structured compiler-style error message with a stable code,
// a primary span, optional note spans, and an optional help string.
type Diagnostic struct {
	Code     string
	Severity Severity
	Title    string
	Primary  Label
	Notes    []Label
	Help     string
}

// Label marks a span of source referenced by Pos with an inline message.
type Label struct {
	Pos token.Pos
	Len int
	Msg string
}
