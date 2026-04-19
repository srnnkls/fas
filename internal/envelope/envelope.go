package envelope

import "encoding/json"

// Category identifies the top-level decision lane selected by the policy
// engine. Exactly one Category is emitted per hook event.
type Category int

const (
	Blocking Category = iota // deny
	Asking                   // ask (+confirm-mode modify)
	Allowing                 // allow (+silent-mode modify)
)

// OutputEnvelope is the adapter-agnostic synthesis output.
// UpdatedInput is dropped (nil) when Category is Blocking.
type OutputEnvelope struct {
	Category          Category
	UserReason        string
	AgentReason       string
	AdditionalContext string
	UpdatedInput      json.RawMessage
}
