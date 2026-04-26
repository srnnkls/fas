package adapter

import (
	"encoding/json"

	"github.com/srnnkls/fas/internal/envelope"
)

// Adapter normalizes vendor hook JSON into the engine's envelope types and
// renders OutputEnvelopes back to vendor-native JSON. Implementations are
// stateless; the hook event name is threaded through RenderOutput so render
// does not depend on a prior ParseInput call.
type Adapter interface {
	Name() string
	ParseInput(raw json.RawMessage) (*envelope.Input, error)
	RenderOutput(out envelope.OutputEnvelope, hookEventName string) (json.RawMessage, error)
	AllowsModify() bool
}
