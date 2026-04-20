// Package cue exposes the embedded CUE sources shipped with the binary so
// that Go packages can evaluate them without re-reading from disk.
package cue

import (
	"embed"
	"io/fs"
)

//go:embed schema.cue
var schemaSource []byte

//go:embed quae.cue flags/*.cue
var stdlibFS embed.FS

// SchemaSource returns the bytes of the shipped `schema.cue` file.
func SchemaSource() []byte {
	return schemaSource
}

// StdlibFS returns the embedded filesystem containing the `quae` CUE stdlib
// (root file plus every `flags/*.cue` helper). Callers mount it into a
// `cue/load` overlay so rule files can resolve
// `import "github.com/srnnkls/quae/cue:quae"` without touching disk.
//
// The returned fs.FS roots at the `cue/` directory: entries look like
// `quae.cue` and `flags/rm.cue`.
func StdlibFS() fs.FS {
	return stdlibFS
}

// StdlibImportPath is the canonical import path rule authors use to pull in
// the embedded stdlib: `import "<StdlibImportPath>"`.
const StdlibImportPath = "github.com/srnnkls/quae/cue:quae"
