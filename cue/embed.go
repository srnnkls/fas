// Package cue exposes the embedded CUE sources shipped with the binary so
// that Go packages can evaluate them without re-reading from disk.
//
// The stdlib is organized as a set of sub-packages under cue/ — hook, tool,
// path, escalation, action, flag — each its own CUE package at its own
// import path. StdlibFS re-embeds the whole tree so the rule loader can
// stage a matching `cue.mod/pkg/...` overlay and resolve per-sub-package
// imports.
package cue

import (
	"embed"
	"io/fs"
)

//go:embed schema.cue
var schemaSource []byte

//go:embed schema.cue hook/*.cue tool/*.cue path/*.cue escalation/*.cue action/*.cue flag/*.cue
var stdlibFS embed.FS

// SchemaSource returns the bytes of the shipped `schema.cue` file. It holds
// the core #Input / #Rule / #Action family consumed by ValidateInput and
// LoadRules via direct cue.CompileBytes — the schema is self-contained and
// does not depend on sub-package imports being resolvable.
func SchemaSource() []byte {
	return schemaSource
}

// StdlibFS returns the embedded filesystem containing every CUE source
// shipped with fas: the core schema.cue plus every sub-package (hook/,
// tool/, path/, escalation/, action/, flag/). Callers mount it into a
// `cue/load` overlay so rule files can resolve
// `import "github.com/srnnkls/fas/cue/<sub>"` without touching disk.
//
// The returned fs.FS roots at the `cue/` directory: entries look like
// `schema.cue`, `hook/events.cue`, and `flag/rm.cue`.
func StdlibFS() fs.FS {
	return stdlibFS
}

// StdlibImportPathPrefix is the canonical import-path prefix each sub-package
// is reachable under. A rule author writes
// `import "<StdlibImportPathPrefix>/hook"` (and so on) to pull in the hook,
// tool, path, escalation, action, or flag sub-package.
const StdlibImportPathPrefix = "github.com/srnnkls/fas/cue"
