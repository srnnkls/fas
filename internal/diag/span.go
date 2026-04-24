package diag

// Span is a serializable position descriptor used across Reason variants in
// place of token.Pos or ast.Node references. Pre-resolving positions to Span
// at construction time preserves JSON round-trip determinism.
type Span struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	Length int    `json:"length"`
}
