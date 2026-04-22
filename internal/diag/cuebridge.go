package diag

import (
	"strings"

	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
)

// DiagError adapts a Diagnostic to Go's standard error contract so load-time
// and lint-time callers can keep using error-returning signatures while
// downstream code recovers the structured diagnostic via errors.As.
//
// The adapter renders through the shared diag.Render pipeline; callers that
// already hold a SourceCache can reuse it, otherwise DiagError lazily builds a
// private FileCache backed by os.ReadFile.
type DiagError struct {
	// D is the structured diagnostic. Consumers reach it via errors.As and
	// then read D.Code, D.Primary, D.Help, etc.
	D Diagnostic
	// Src supplies source lines for the renderer. When nil, Error() falls
	// back to an internal FileCache that reads from disk on first use.
	Src SourceCache
	// Cause is the underlying error, if any. Preserved so errors.Unwrap and
	// errors.Is / errors.As can keep traversing into CUE's own error tree —
	// existing callers type-assert for cueerrors.Error and that chain must
	// stay intact across the migration.
	Cause error
}

// defaultCache is shared across DiagError instances that did not receive a
// caller-supplied cache; disk reads are memoised for the life of the process.
var defaultCache = NewFileCache()

// Error renders the diagnostic as a multi-line Rust-style string using the
// attached SourceCache (or a process-wide file cache as a fallback).
func (e *DiagError) Error() string {
	if e == nil {
		return ""
	}
	src := e.Src
	if src == nil {
		src = defaultCache
	}
	return Render(e.D, src)
}

// Unwrap surfaces the underlying error so errors.Is / errors.As continue to
// traverse into CUE's error tree.
func (e *DiagError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewDiagError constructs a DiagError from a Diagnostic; Src and Cause may be
// nil. The nil-guarded constructor gives callers a single spelling rather than
// spreading literal composite constructions across packages.
func NewDiagError(d Diagnostic, src SourceCache, cause error) *DiagError {
	return &DiagError{D: d, Src: src, Cause: cause}
}

// FromCueError converts a CUE diagnostic chain into a structured Diagnostic.
//
// Classification uses two signals from the CUE error tree:
//
//   - Path shape. A path ending at `then` means the author's `then:` value
//     does not satisfy the `#Action` schema as a whole (a scalar where a
//     struct is required — E0101). A path that extends past `then` —
//     e.g. `[#Rule then halt]` — names a child field that does not exist in
//     the closed `#Action` schema (unknown action kind — E0102).
//   - Message shape. "field not allowed" reinforces the unknown-kind
//     classification; "conflicting values" and "errors in empty disjunction"
//     reinforce the shape-mismatch classification. Message-based signals are
//     secondary so the classifier stays robust if CUE reshapes its path
//     reporting in a future release.
//
// Non-CUE errors (e.g. the plain fmt.Errorf values produced by the lint pass
// until T5 lands) fall through to a generic E0101 diagnostic that carries the
// message as its Title with no source position.
func FromCueError(err error) Diagnostic {
	d := Diagnostic{
		Code:     E0101.Code,
		Severity: SeverityError,
		Help:     E0101.Help,
	}
	if err == nil {
		return d
	}

	var cueErr cueerrors.Error
	if !cueerrors.As(err, &cueErr) {
		// Non-CUE errors land here — preserve the full text in the Title so
		// rendered output still carries the original classification words
		// (lint-emitted phrases like "cross", "unbound", "self-ref").
		d.Title = err.Error()
		return d
	}

	leaves := cueerrors.Errors(cueErr)
	code, title := classifyCueErrors(leaves)
	d.Code = code
	d.Title = title
	if info, ok := LookupCode(code); ok {
		d.Help = info.Help
	}
	if pos := pickPrimaryPos(leaves); pos.IsValid() {
		d.Primary = Label{Pos: pos, Msg: title}
	}
	return d
}

// classifyCueErrors walks the leaf diagnostics in order and returns the first
// (code, title) pair that matches a known classifier. Order matters: E0102's
// "field not allowed" / path-extension branch is more specific than E0101's
// disjunction-level "conflicting values", so the kind-level check runs first.
func classifyCueErrors(leaves []cueerrors.Error) (code, title string) {
	for _, e := range leaves {
		if isUnknownActionKind(e) {
			return E0102.Code, humanizeLeaf(e)
		}
	}
	for _, e := range leaves {
		if isSchemaShapeMismatch(e) {
			return E0101.Code, humanizeLeaf(e)
		}
	}
	if len(leaves) > 0 {
		return E0101.Code, humanizeLeaf(leaves[0])
	}
	return E0101.Code, ""
}

// isUnknownActionKind reports whether a CUE leaf names a field under `then`
// that is not part of the closed `#Action` schema. The path shape — `[#Rule
// then <kind>]` — carries the primary signal; the "field not allowed" message
// text is a secondary signal that stays correct if CUE's path reporting
// changes in a future release.
func isUnknownActionKind(e cueerrors.Error) bool {
	if hasThenChildInPath(e.Path()) {
		return true
	}
	format, _ := e.Msg()
	return strings.Contains(format, "field not allowed")
}

// isSchemaShapeMismatch reports whether a CUE leaf complains that `then` as a
// whole does not satisfy `#Action`. The disjunction-level "errors in empty
// disjunction" wrapper and the per-arm "conflicting values" leaves both land
// here; either is enough to classify the failure as an E0101.
func isSchemaShapeMismatch(e cueerrors.Error) bool {
	format, _ := e.Msg()
	if strings.Contains(format, "errors in empty disjunction") {
		return true
	}
	if strings.Contains(format, "conflicting values") {
		return true
	}
	return pathEndsAtThen(e.Path())
}

// hasThenChildInPath reports whether the path contains a `then` segment
// followed by at least one more segment — the error points at a named child
// of `then`, not at `then` itself.
func hasThenChildInPath(path []string) bool {
	for i, seg := range path {
		if seg == "then" && i+1 < len(path) {
			return true
		}
	}
	return false
}

// pathEndsAtThen reports whether the last segment of the error's path is
// exactly `then`.
func pathEndsAtThen(path []string) bool {
	if len(path) == 0 {
		return false
	}
	return path[len(path)-1] == "then"
}

// pickPrimaryPos returns the best source position for a rendering caret. The
// first input position that lives in a user-authored file (i.e. not the
// embedded schema) wins; if none are available, fall back to the leaf's
// Position(), which may be invalid.
func pickPrimaryPos(leaves []cueerrors.Error) token.Pos {
	for _, e := range leaves {
		for _, p := range e.InputPositions() {
			if !p.IsValid() {
				continue
			}
			if isSchemaFile(p.Filename()) {
				continue
			}
			return p
		}
	}
	for _, e := range leaves {
		if p := e.Position(); p.IsValid() {
			return p
		}
	}
	return token.NoPos
}

// isSchemaFile reports whether a filename belongs to the embedded CUE schema
// bundle. Positions in schema files are noise from the author's perspective;
// the caret belongs in the rule file.
func isSchemaFile(name string) bool {
	base := name
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	switch base {
	case "schema.cue", "input.cue":
		return true
	}
	return false
}

// humanizeLeaf returns a single-line description of a CUE leaf suitable for
// the Diagnostic's Title. The full formatted body — paths included — is
// preserved up to the first newline so existing substring assertions still
// match once rendered. Render re-inflates the diagnostic into its multi-line
// shape; Title is a one-liner by contract.
func humanizeLeaf(e cueerrors.Error) string {
	msg := strings.TrimSpace(e.Error())
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}
