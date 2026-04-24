package diag

import (
	"bytes"
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"
)

// Stable JSON tags keyed per variant; they outlive Go type renames and CUE
// version upgrades.
const (
	tagKindMismatch      = "kind_mismatch"
	tagBoundViolation    = "bound_violation"
	tagRegexMismatch     = "regex_mismatch"
	tagConjunctFailed    = "conjunct_failed"
	tagDisjunctionFailed = "disjunction_failed"
	tagKeyMissing        = "key_missing"
	tagProvenance        = "provenance"
)

// kindNames stabilises cue.Kind across JSON boundaries with canonical
// lowercase names rather than the underlying integer constant.
var kindNames = map[cue.Kind]string{
	cue.NullKind:   "null",
	cue.BoolKind:   "bool",
	cue.IntKind:    "int",
	cue.FloatKind:  "float",
	cue.NumberKind: "number",
	cue.StringKind: "string",
	cue.BytesKind:  "bytes",
	cue.ListKind:   "list",
	cue.StructKind: "struct",
}

var kindByName = func() map[string]cue.Kind {
	m := make(map[string]cue.Kind, len(kindNames))
	for k, s := range kindNames {
		m[s] = k
	}
	return m
}()

func kindToString(k cue.Kind) string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	// Fallback: rely on cue.Kind's own String method for any composite kind.
	return k.String()
}

func stringToKind(s string) (cue.Kind, error) {
	if k, ok := kindByName[s]; ok {
		return k, nil
	}
	return 0, fmt.Errorf("diag: unknown cue.Kind string %q", s)
}

// Wire structs — tag order encodes JSON key order.

type kindMismatchWire struct {
	Type   string `json:"type"`
	Want   string `json:"want"`
	Got    string `json:"got"`
	Actual string `json:"actual"`
}

type boundViolationWire struct {
	Type     string `json:"type"`
	Op       string `json:"op"`
	Bound    string `json:"bound"`
	Actual   string `json:"actual"`
	Distance string `json:"distance"`
}

type regexMismatchWire struct {
	Type      string `json:"type"`
	Pattern   string `json:"pattern"`
	Input     string `json:"input"`
	DivergeAt int    `json:"diverge_at"`
}

type conjunctFailedWire struct {
	Type string          `json:"type"`
	Expr string          `json:"expr"`
	Span Span            `json:"span"`
	Sub  json.RawMessage `json:"sub"`
}

type armResultWire struct {
	Arm   string          `json:"arm"`
	Span  Span            `json:"span"`
	Inner json.RawMessage `json:"inner"`
	Score int             `json:"score"`
}

type disjunctionFailedWire struct {
	Type string          `json:"type"`
	Arms []armResultWire `json:"arms"`
}

type keyMissingWire struct {
	Type          string   `json:"type"`
	Key           string   `json:"key"`
	AvailableKeys []string `json:"available_keys"`
	Suggestion    string   `json:"suggestion"`
}

type provenanceWire struct {
	Type    string `json:"type"`
	Span    Span   `json:"span"`
	Snippet string `json:"snippet"`
}

func (r KindMismatch) MarshalJSON() ([]byte, error) {
	return json.Marshal(kindMismatchWire{
		Type: tagKindMismatch, Want: kindToString(r.Want), Got: kindToString(r.Got), Actual: r.Actual,
	})
}

func (r BoundViolation) MarshalJSON() ([]byte, error) {
	return json.Marshal(boundViolationWire{
		Type: tagBoundViolation, Op: r.Op, Bound: r.Bound, Actual: r.Actual, Distance: r.Distance,
	})
}

func (r RegexMismatch) MarshalJSON() ([]byte, error) {
	return json.Marshal(regexMismatchWire{
		Type: tagRegexMismatch, Pattern: r.Pattern, Input: r.Input, DivergeAt: r.DivergeAt,
	})
}

func (r ConjunctFailed) MarshalJSON() ([]byte, error) {
	sub, err := marshalReason(r.Sub)
	if err != nil {
		return nil, err
	}
	return json.Marshal(conjunctFailedWire{
		Type: tagConjunctFailed, Expr: r.Expr, Span: r.Span, Sub: sub,
	})
}

func (r DisjunctionFailed) MarshalJSON() ([]byte, error) {
	arms := make([]armResultWire, len(r.Arms))
	for i, a := range r.Arms {
		inner, err := marshalReason(a.Inner)
		if err != nil {
			return nil, err
		}
		arms[i] = armResultWire{
			Arm: a.Arm, Span: a.Span, Inner: inner, Score: a.Score,
		}
	}
	return json.Marshal(disjunctionFailedWire{Type: tagDisjunctionFailed, Arms: arms})
}

func (r KeyMissing) MarshalJSON() ([]byte, error) {
	keys := r.AvailableKeys
	if keys == nil {
		keys = []string{}
	}
	return json.Marshal(keyMissingWire{
		Type: tagKeyMissing, Key: r.Key, AvailableKeys: keys, Suggestion: r.Suggestion,
	})
}

func (r Provenance) MarshalJSON() ([]byte, error) {
	return json.Marshal(provenanceWire{
		Type: tagProvenance, Span: r.Span, Snippet: r.Snippet,
	})
}

// marshalReason returns the JSON encoding of a possibly-nil Reason as a raw
// message, so parent structs can embed it without a double-encoding wrapper.
func marshalReason(r Reason) (json.RawMessage, error) {
	if r == nil {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(r)
}

// UnmarshalReason dispatches on the "type" tag to construct the concrete
// Reason variant. It rejects missing or unknown tags so future-variant JSON
// does not silently flow through older readers.
func UnmarshalReason(data []byte) (Reason, error) {
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("diag: UnmarshalReason: %w", err)
	}
	if probe.Type == "" {
		return nil, fmt.Errorf("diag: UnmarshalReason: missing %q tag", "type")
	}
	switch probe.Type {
	case tagKindMismatch:
		var w kindMismatchWire
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("diag: unmarshal reason %q: %w", probe.Type, err)
		}
		want, err := stringToKind(w.Want)
		if err != nil {
			return nil, err
		}
		got, err := stringToKind(w.Got)
		if err != nil {
			return nil, err
		}
		return KindMismatch{Want: want, Got: got, Actual: w.Actual}, nil
	case tagBoundViolation:
		var w boundViolationWire
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("diag: unmarshal reason %q: %w", probe.Type, err)
		}
		return BoundViolation{Op: w.Op, Bound: w.Bound, Actual: w.Actual, Distance: w.Distance}, nil
	case tagRegexMismatch:
		var w regexMismatchWire
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("diag: unmarshal reason %q: %w", probe.Type, err)
		}
		return RegexMismatch{Pattern: w.Pattern, Input: w.Input, DivergeAt: w.DivergeAt}, nil
	case tagConjunctFailed:
		var w conjunctFailedWire
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("diag: unmarshal reason %q: %w", probe.Type, err)
		}
		sub, err := UnmarshalReason(w.Sub)
		if err != nil {
			return nil, err
		}
		return ConjunctFailed{Expr: w.Expr, Span: w.Span, Sub: sub}, nil
	case tagDisjunctionFailed:
		var w disjunctionFailedWire
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("diag: unmarshal reason %q: %w", probe.Type, err)
		}
		arms := make([]ArmResult, len(w.Arms))
		for i, a := range w.Arms {
			inner, err := UnmarshalReason(a.Inner)
			if err != nil {
				return nil, err
			}
			arms[i] = ArmResult{Arm: a.Arm, Span: a.Span, Inner: inner, Score: a.Score}
		}
		// Preserve empty-slice shape across round-trips.
		if w.Arms != nil && len(arms) == 0 {
			arms = []ArmResult{}
		}
		return DisjunctionFailed{Arms: arms}, nil
	case tagKeyMissing:
		var w keyMissingWire
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("diag: unmarshal reason %q: %w", probe.Type, err)
		}
		keys := w.AvailableKeys
		if keys == nil {
			keys = []string{}
		}
		return KeyMissing{Key: w.Key, AvailableKeys: keys, Suggestion: w.Suggestion}, nil
	case tagProvenance:
		var w provenanceWire
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("diag: unmarshal reason %q: %w", probe.Type, err)
		}
		return Provenance{Span: w.Span, Snippet: w.Snippet}, nil
	}
	return nil, fmt.Errorf("diag: UnmarshalReason: unknown type %q", probe.Type)
}

type labelWire struct {
	Span    Span              `json:"span"`
	Len     int               `json:"len"`
	Msg     string            `json:"msg"`
	Reasons []json.RawMessage `json:"reasons,omitempty"`
}

// spanFromPos resolves a token.Pos to a Span DTO. token.NoPos yields the zero
// Span; callers that need non-zero spans for valid positions must supply the
// Length separately (carried on Label.Len, not on the Span itself here).
func spanFromPos(p token.Pos, length int) Span {
	if !p.IsValid() {
		return Span{Length: length}
	}
	return Span{
		File:   p.Filename(),
		Line:   p.Line(),
		Col:    p.Column(),
		Length: length,
	}
}

// MarshalJSON emits the Label envelope. Reasons is omitted when nil or empty
// so the NF5 fallthrough Label shape stays compact.
func (l Label) MarshalJSON() ([]byte, error) {
	span := spanFromPos(l.Pos, l.Len)
	w := labelWire{Span: span, Len: l.Len, Msg: l.Msg}
	if len(l.Reasons) > 0 {
		w.Reasons = make([]json.RawMessage, len(l.Reasons))
		for i, r := range l.Reasons {
			raw, err := marshalReason(r)
			if err != nil {
				return nil, err
			}
			w.Reasons[i] = raw
		}
	}
	return json.Marshal(w)
}

// UnmarshalJSON parses the Label envelope. Positions cannot be reconstructed
// without a token.FileSet; Pos stays at NoPos after decoding, which is
// round-trip safe because a zero Span remarshals to the same bytes.
func (l *Label) UnmarshalJSON(data []byte) error {
	var w labelWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	l.Pos = token.NoPos
	l.Len = w.Len
	l.Msg = w.Msg
	if len(w.Reasons) == 0 {
		l.Reasons = nil
		return nil
	}
	l.Reasons = make([]Reason, len(w.Reasons))
	for i, raw := range w.Reasons {
		r, err := UnmarshalReason(raw)
		if err != nil {
			return err
		}
		l.Reasons[i] = r
	}
	return nil
}
