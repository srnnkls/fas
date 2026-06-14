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

// Continuation is the harness-neutral "do not end the turn — hand this reason
// back to the agent" lane, distinct from the permission Categories. A turn-end
// event (Stop/SubagentStop) renders it as the harness's block-and-continue
// contract; other events ignore it. RuleID names the firing rule.
type Continuation struct {
	RuleID string
	Reason string
}

// OutputEnvelope is the adapter-agnostic synthesis output.
// UpdatedInput is dropped (nil) when Category is Blocking.
// Continuation is nil when no continuation rule fired.
type OutputEnvelope struct {
	Category          Category
	UserReason        string
	AgentReason       string
	AdditionalContext string
	UpdatedInput      json.RawMessage
	Continuation      *Continuation
}

// WithContinuationGuard returns the envelope with its Continuation lane cleared
// when alreadyContinuing is true — the stateless R3 loop guard. fas keeps no
// cross-turn counter; the harness supplies the "already continuing" signal.
func (o OutputEnvelope) WithContinuationGuard(alreadyContinuing bool) OutputEnvelope {
	if alreadyContinuing {
		o.Continuation = nil
	}
	return o
}
