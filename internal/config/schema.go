package config

import (
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue"

	quaecue "github.com/srnnkls/quae/cue"
)

// SchemaSource returns the bytes of the embedded `schema.cue` file so other
// packages can reuse the shipped schema without re-reading it from disk.
func SchemaSource() []byte {
	return quaecue.SchemaSource()
}

// ValidateInput unifies raw adapter JSON against the `#Input` CUE schema as a
// defense-in-depth check before the evaluator runs. It returns a non-nil
// error when the input fails to satisfy the schema; the error names the
// violated constraint.
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
