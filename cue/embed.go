// Package cue exposes the embedded CUE sources shipped with the binary so
// that Go packages can evaluate them without re-reading from disk.
package cue

import _ "embed"

//go:embed schema.cue
var schemaSource []byte

// SchemaSource returns the bytes of the shipped `schema.cue` file.
func SchemaSource() []byte {
	return schemaSource
}
