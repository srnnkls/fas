package config

import (
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// schemaBundle caches the compiled schema together with the pre-looked-up
// `#Input` and `#Rule` values so hot paths (ValidateInput, LoadRules) avoid
// re-parsing schema.cue on every call.
type schemaBundle struct {
	ctx      *cue.Context
	schema   cue.Value
	inputDef cue.Value
	ruleDef  cue.Value
}

var (
	schemaOnce   sync.Once
	cachedBundle schemaBundle
	cachedErr    error
)

// loadSchema returns the cached schema bundle. The first call compiles
// schema.cue and performs definition lookups; subsequent calls reuse both
// the cue.Context and the pre-resolved values.
func loadSchema() (schemaBundle, error) {
	schemaOnce.Do(func() {
		ctx := cuecontext.New()
		schema := ctx.CompileBytes(SchemaSource(), cue.Filename("schema.cue"))
		if err := schema.Err(); err != nil {
			cachedErr = fmt.Errorf("compile schema.cue: %w", err)
			return
		}
		inputDef := schema.LookupPath(cue.ParsePath("#Input"))
		if err := inputDef.Err(); err != nil {
			cachedErr = fmt.Errorf("lookup #Input: %w", err)
			return
		}
		ruleDef := schema.LookupPath(cue.ParsePath("#Rule"))
		if err := ruleDef.Err(); err != nil {
			cachedErr = fmt.Errorf("lookup #Rule: %w", err)
			return
		}
		cachedBundle = schemaBundle{
			ctx:      ctx,
			schema:   schema,
			inputDef: inputDef,
			ruleDef:  ruleDef,
		}
	})
	return cachedBundle, cachedErr
}
