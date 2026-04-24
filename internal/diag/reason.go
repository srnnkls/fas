package diag

import "cuelang.org/go/cue"

// Reason is the sealed sum type carrying the structured cause of a
// diagnostic. Implementations are enumerated in this package and sealed by an
// unexported marker method.
type Reason interface {
	reason()
}

// KindMismatch reports a kind-lattice disjointness between the expected and
// actual values at a leaf.
type KindMismatch struct {
	Want   cue.Kind
	Got    cue.Kind
	Actual string
}

func (KindMismatch) reason() {}

// BoundViolation reports a failed numeric or lexicographic bound check.
type BoundViolation struct {
	Op       string
	Bound    string
	Actual   string
	Distance string
}

func (BoundViolation) reason() {}

// RegexMismatch reports a regex constraint failure. DivergeAt is the byte
// offset at which the longest-matching prefix ended; -1 if unavailable.
type RegexMismatch struct {
	Pattern   string
	Input     string
	DivergeAt int
}

func (RegexMismatch) reason() {}

// ConjunctFailed reports one failing conjunct of a unified constraint.
// Multiplicity at a single leaf is represented by multiple ConjunctFailed
// entries on Label.Reasons (invariant I3a).
type ConjunctFailed struct {
	Expr string
	Span Span
	Sub  Reason
}

func (ConjunctFailed) reason() {}

// ArmResult describes one arm of a failing disjunction.
type ArmResult struct {
	Arm   string
	Span  Span
	Inner Reason
	Score int
}

// DisjunctionFailed reports that no arm of a disjunction matched the input.
// Arms is ordered by Score descending; source order breaks ties.
type DisjunctionFailed struct {
	Arms []ArmResult
}

func (DisjunctionFailed) reason() {}

// KeyMissing reports an absent required key. Suggestion is the closest
// available key by edit distance (≤ 2), or empty when no close match exists.
type KeyMissing struct {
	Key           string
	AvailableKeys []string
	Suggestion    string
}

func (KeyMissing) reason() {}

// Provenance is a metadata Reason surfacing where a constraint was
// introduced, used on footer labels to show cross-file origin.
type Provenance struct {
	Span    Span
	Snippet string
}

func (Provenance) reason() {}
