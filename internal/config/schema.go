package config

import (
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue"

	fascue "github.com/srnnkls/fas/cue"
)

// SchemaSource returns the bytes of the embedded `schema.cue` file so other
// packages can reuse the shipped schema without re-reading it from disk.
func SchemaSource() []byte {
	return fascue.SchemaSource()
}

// ValidateInput unifies raw adapter JSON against `#Input` and returns a
// non-nil error naming the violated constraint. It is a CUE-level check used
// in tests, not a runtime gate: `fas eval` does not call it — the eval path
// gates only on each rule's `when`, staying permissive about wire fields it
// does not police so a new Claude Code hook event cannot break evaluation.
func ValidateInput(raw []byte) error {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("decode input json: %w", err)
	}

	bundle, err := loadSchema()
	if err != nil {
		return err
	}

	value := bundle.ctx.Encode(decoded)
	if err := value.Err(); err != nil {
		return fmt.Errorf("encode input value: %w", err)
	}

	unified := bundle.inputDef.Unify(value)
	if err := unified.Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("input does not satisfy #Input: %w", err)
	}
	return nil
}
