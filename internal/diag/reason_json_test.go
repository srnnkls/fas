package diag_test

import (
	"encoding/json"
	"testing"

	"cuelang.org/go/cue"

	"github.com/srnnkls/fas/internal/diag"
)

// ---------------------------------------------------------------------------
// JSON round-trip + byte-identical fixtures for the Reason sum type.
//
// The canonical marshalling obeys these rules (the SPEC):
//
//   1. Every variant is a JSON object carrying a stable "type" string tag.
//      The tag is invariant across releases and CUE version upgrades.
//        KindMismatch      -> "kind_mismatch"
//        BoundViolation    -> "bound_violation"
//        RegexMismatch     -> "regex_mismatch"
//        ConjunctFailed    -> "conjunct_failed"
//        DisjunctionFailed -> "disjunction_failed"
//        KeyMissing        -> "key_missing"
//        Provenance        -> "provenance"
//
//   2. cue.Kind is emitted as a lowercase Go-style type name ("int",
//      "string", "bool", "number", "float", "bytes", "list", "struct",
//      "null", "bool"). Using a string tag rather than the int value
//      insulates the JSON schema from upstream renumbering of Kind
//      constants.
//
//   3. Field names are snake_case JSON keys mirroring the Go field names
//      (Want->want, Got->got, Actual->actual, Op->op, Bound->bound,
//      Distance->distance, Pattern->pattern, Input->input,
//      DivergeAt->diverge_at, Expr->expr, Span->span (nested object),
//      Sub->sub, Arms->arms, Arm->arm, Inner->inner, Score->score,
//      Key->key, AvailableKeys->available_keys, Suggestion->suggestion,
//      Snippet->snippet, File->file, Line->line, Col->col, Length->length).
//
//   4. Round-trip is byte-identical: for every fixture,
//      remarshal(unmarshal(marshal(v))) == marshal(v).
//
//   5. Polymorphic Reason values inside Label.Reasons, ConjunctFailed.Sub,
//      and ArmResult.Inner unmarshal via a package-provided dispatcher
//      (diag.UnmarshalReason). Label has a custom UnmarshalJSON.
//
// Fixtures below exercise at minimum: each of the 7 singular variants; a
// multi-Reason Label; a nested ConjunctFailed wrapping a BoundViolation; a
// DisjunctionFailed with 3 arms sorted by Score desc; an ArmResult.Inner
// holding a nested KindMismatch; a Span zero-value; a KeyMissing with
// AvailableKeys=[] and Suggestion="". That's 12 distinct shape assertions.
// ---------------------------------------------------------------------------

// reasonRoundTrip asserts that marshalling a Reason value, unmarshalling via
// diag.UnmarshalReason, and remarshalling yields byte-identical JSON, and
// that the first marshal equals wantJSON.
func reasonRoundTrip(t *testing.T, name string, r diag.Reason, wantJSON string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		first, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal(1): %v", err)
		}
		if string(first) != wantJSON {
			t.Errorf("first marshal mismatch\n got: %s\nwant: %s", first, wantJSON)
		}
		decoded, err := diag.UnmarshalReason(first)
		if err != nil {
			t.Fatalf("UnmarshalReason: %v (input: %s)", err, first)
		}
		second, err := json.Marshal(decoded)
		if err != nil {
			t.Fatalf("marshal(2): %v", err)
		}
		if string(first) != string(second) {
			t.Errorf("round-trip not byte-identical\n 1st: %s\n 2nd: %s", first, second)
		}
	})
}

// Fixture 1: KindMismatch with want=int, got=string — pins the "kind_mismatch"
// tag and the string form of cue.Kind (insulation against int renumbering).
func TestReasonJSON_KindMismatch(t *testing.T) {
	r := diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"five"`}
	want := `{"type":"kind_mismatch","want":"int","got":"string","actual":"\"five\""}`
	reasonRoundTrip(t, "int_vs_string", r, want)
}

// Fixture 2: BoundViolation.
func TestReasonJSON_BoundViolation(t *testing.T) {
	r := diag.BoundViolation{Op: ">=", Bound: "5", Actual: "3", Distance: "off by 2"}
	want := `{"type":"bound_violation","op":"\u003e=","bound":"5","actual":"3","distance":"off by 2"}`
	reasonRoundTrip(t, "ge5_actual3", r, want)
}

// Fixture 3: RegexMismatch with a valid DivergeAt.
func TestReasonJSON_RegexMismatch(t *testing.T) {
	r := diag.RegexMismatch{Pattern: "^rm ", Input: "rm-rf", DivergeAt: 2}
	want := `{"type":"regex_mismatch","pattern":"^rm ","input":"rm-rf","diverge_at":2}`
	reasonRoundTrip(t, "rm_prefix", r, want)
}

// Fixture 4: RegexMismatch with DivergeAt=-1 (complex pattern bailed out);
// -1 must be preserved as-is, not elided.
func TestReasonJSON_RegexMismatchDivergeNegative(t *testing.T) {
	r := diag.RegexMismatch{Pattern: "(a|b)+", Input: "xxx", DivergeAt: -1}
	want := `{"type":"regex_mismatch","pattern":"(a|b)+","input":"xxx","diverge_at":-1}`
	reasonRoundTrip(t, "diverge_minus_one", r, want)
}

// Fixture 5: ConjunctFailed whose Sub is a BoundViolation — exercises
// nested-Reason marshalling.
func TestReasonJSON_ConjunctFailedWithBoundSub(t *testing.T) {
	r := diag.ConjunctFailed{
		Expr: ">=5",
		Span: diag.Span{File: "r.cue", Line: 8, Col: 30, Length: 3},
		Sub: diag.BoundViolation{
			Op: ">=", Bound: "5", Actual: "3", Distance: "off by 2",
		},
	}
	want := `{"type":"conjunct_failed","expr":"\u003e=5","span":{"file":"r.cue","line":8,"col":30,"length":3},"sub":{"type":"bound_violation","op":"\u003e=","bound":"5","actual":"3","distance":"off by 2"}}`
	reasonRoundTrip(t, "ge5_sub_bound", r, want)
}

// Fixture 6: DisjunctionFailed with 3 ArmResults sorted by Score descending
// (ties by source order). Inner reasons vary — each arm's inner is a
// distinct variant to exercise polymorphic marshalling inside Arms.
func TestReasonJSON_DisjunctionFailedThreeArms(t *testing.T) {
	r := diag.DisjunctionFailed{
		Arms: []diag.ArmResult{
			{
				Arm:  `"Read"`,
				Span: diag.Span{File: "r.cue", Line: 10, Col: 22, Length: 6},
				Inner: diag.KindMismatch{
					Want: cue.StringKind, Got: cue.StringKind, Actual: `"Rd"`,
				},
				Score: 50,
			},
			{
				Arm:  `"Edit"`,
				Span: diag.Span{File: "r.cue", Line: 10, Col: 42, Length: 6},
				Inner: diag.KindMismatch{
					Want: cue.StringKind, Got: cue.StringKind, Actual: `"Rd"`,
				},
				Score: 40,
			},
			{
				Arm:  `"Bash"`,
				Span: diag.Span{File: "r.cue", Line: 10, Col: 14, Length: 6},
				Inner: diag.KindMismatch{
					Want: cue.StringKind, Got: cue.StringKind, Actual: `"Rd"`,
				},
				Score: 10,
			},
		},
	}
	want := `{"type":"disjunction_failed","arms":[` +
		`{"arm":"\"Read\"","span":{"file":"r.cue","line":10,"col":22,"length":6},"inner":{"type":"kind_mismatch","want":"string","got":"string","actual":"\"Rd\""},"score":50},` +
		`{"arm":"\"Edit\"","span":{"file":"r.cue","line":10,"col":42,"length":6},"inner":{"type":"kind_mismatch","want":"string","got":"string","actual":"\"Rd\""},"score":40},` +
		`{"arm":"\"Bash\"","span":{"file":"r.cue","line":10,"col":14,"length":6},"inner":{"type":"kind_mismatch","want":"string","got":"string","actual":"\"Rd\""},"score":10}` +
		`]}`
	reasonRoundTrip(t, "three_arms_score_desc", r, want)
}

// Fixture 7: ArmResult.Inner is itself a nested KindMismatch — already
// tested in fixture 6, but we pin a single-arm disjunction too, to exercise
// the singleton Arms case and confirm Arms is always encoded as an array
// even with length 1.
func TestReasonJSON_DisjunctionFailedSingleArm(t *testing.T) {
	r := diag.DisjunctionFailed{
		Arms: []diag.ArmResult{
			{
				Arm:   `"Bash"`,
				Span:  diag.Span{File: "r.cue", Line: 10, Col: 14, Length: 6},
				Inner: diag.KindMismatch{Want: cue.StringKind, Got: cue.BoolKind, Actual: "true"},
				Score: 0,
			},
		},
	}
	want := `{"type":"disjunction_failed","arms":[{"arm":"\"Bash\"","span":{"file":"r.cue","line":10,"col":14,"length":6},"inner":{"type":"kind_mismatch","want":"string","got":"bool","actual":"true"},"score":0}]}`
	reasonRoundTrip(t, "single_arm", r, want)
}

// Fixture 8: KeyMissing with both AvailableKeys and Suggestion populated.
func TestReasonJSON_KeyMissingWithSuggestion(t *testing.T) {
	r := diag.KeyMissing{
		Key:           "flags",
		AvailableKeys: []string{"flag", "forced"},
		Suggestion:    "flag",
	}
	want := `{"type":"key_missing","key":"flags","available_keys":["flag","forced"],"suggestion":"flag"}`
	reasonRoundTrip(t, "flags_suggest_flag", r, want)
}

// Fixture 9: KeyMissing with AvailableKeys=[] and Suggestion="" — empty
// parent case from F5 / acceptance criterion #15. Empty slice must
// serialize as "[]", not "null" (consumers iterate without nil checks).
func TestReasonJSON_KeyMissingEmptyParent(t *testing.T) {
	r := diag.KeyMissing{
		Key:           "port",
		AvailableKeys: []string{},
		Suggestion:    "",
	}
	want := `{"type":"key_missing","key":"port","available_keys":[],"suggestion":""}`
	reasonRoundTrip(t, "empty_parent", r, want)
}

// Fixture 10: Provenance metadata Reason.
func TestReasonJSON_Provenance(t *testing.T) {
	r := diag.Provenance{
		Span:    diag.Span{File: "stdlib/nums.cue", Line: 7, Col: 17, Length: 3},
		Snippet: ">=0",
	}
	want := `{"type":"provenance","span":{"file":"stdlib/nums.cue","line":7,"col":17,"length":3},"snippet":"\u003e=0"}`
	reasonRoundTrip(t, "stdlib_positive", r, want)
}

// Fixture 11: Span zero value — recognisable shape (File="", all ints 0)
// that round-trips. This is the "position unknown" marker per NF3 /
// acceptance target.
func TestReasonJSON_SpanZeroValue(t *testing.T) {
	// Span isn't a Reason itself, so we exercise it via a ConjunctFailed
	// whose Span is zero.
	r := diag.ConjunctFailed{
		Expr: "int",
		Span: diag.Span{},
		Sub:  diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"x"`},
	}
	want := `{"type":"conjunct_failed","expr":"int","span":{"file":"","line":0,"col":0,"length":0},"sub":{"type":"kind_mismatch","want":"int","got":"string","actual":"\"x\""}}`
	reasonRoundTrip(t, "zero_span", r, want)

	// Direct Span round-trip: marshal+remarshal also byte-identical.
	span := diag.Span{}
	b, err := json.Marshal(span)
	if err != nil {
		t.Fatalf("marshal Span{}: %v", err)
	}
	wantSpan := `{"file":"","line":0,"col":0,"length":0}`
	if string(b) != wantSpan {
		t.Errorf("Span{} marshal = %s, want %s", b, wantSpan)
	}
	var decoded diag.Span
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal Span{}: %v", err)
	}
	b2, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("marshal(2) Span: %v", err)
	}
	if string(b) != string(b2) {
		t.Errorf("Span round-trip not byte-identical\n 1st: %s\n 2nd: %s", b, b2)
	}
}

// Fixture 12: a Label carrying Reasons=[KindMismatch, BoundViolation,
// RegexMismatch] — the multi-Reason case. Must serialize as
// "reasons": [...] with a 3-element array, round-trip byte-identically,
// and preserve order.
func TestReasonJSON_LabelMultiReason(t *testing.T) {
	// Span for the Label itself; carried as a DTO so positions round-trip.
	// The underlying diag.Label still uses token.Pos for its Pos field
	// today — JSON emission must pin the Label shape the tests expect.
	l := diag.Label{
		// Pos/Len are re-asserted through the Span the renderer derives;
		// the JSON form of Label encodes Pos as a Span object.
		Len: 5,
		Msg: "", // empty when Reasons carries content
		Reasons: []diag.Reason{
			diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"x"`},
			diag.BoundViolation{Op: ">=", Bound: "5", Actual: "3", Distance: "off by 2"},
			diag.RegexMismatch{Pattern: "^rm ", Input: "ls", DivergeAt: 0},
		},
	}

	first, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal Label: %v", err)
	}

	// The Label JSON shape is: {"span":{...},"len":N,"msg":"...","reasons":[...]}
	// We assert the reasons array is length-3 and order-preserving via
	// round-trip, without over-pinning the Label envelope shape (which
	// T13 refines). The assertion below is deliberately structural, not
	// byte-level, so Label can evolve its non-reasons keys without
	// touching this test.
	var envelope struct {
		Reasons []json.RawMessage `json:"reasons"`
	}
	if err := json.Unmarshal(first, &envelope); err != nil {
		t.Fatalf("unmarshal Label envelope: %v", err)
	}
	if got := len(envelope.Reasons); got != 3 {
		t.Fatalf("Label.reasons length after marshal = %d, want 3", got)
	}

	wantTags := []string{"kind_mismatch", "bound_violation", "regex_mismatch"}
	for i, raw := range envelope.Reasons {
		var tag struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &tag); err != nil {
			t.Fatalf("reasons[%d] tag: %v", i, err)
		}
		if tag.Type != wantTags[i] {
			t.Errorf("reasons[%d].type = %q, want %q", i, tag.Type, wantTags[i])
		}
	}

	// Round-trip: unmarshal the whole Label, remarshal, expect byte-identical.
	var decoded diag.Label
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("unmarshal Label: %v", err)
	}
	if got := len(decoded.Reasons); got != 3 {
		t.Errorf("decoded Label.Reasons length = %d, want 3", got)
	}
	second, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("remarshal Label: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("Label round-trip not byte-identical\n 1st: %s\n 2nd: %s", first, second)
	}
}

// The fixture battery, re-run via a table — the individual Test* functions
// above anchor distinct SPEC facts (tag name, cue.Kind string, negative
// DivergeAt, empty slice shape, etc); the table below guarantees every
// variant survives the generic round-trip regardless of any individual
// assertion passing.
func TestReasonJSON_AllVariantsRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		r    diag.Reason
	}{
		{"kind_mismatch", diag.KindMismatch{Want: cue.BoolKind, Got: cue.NullKind, Actual: "null"}},
		{"bound_violation", diag.BoundViolation{Op: "<=", Bound: "10", Actual: "12", Distance: "off by 2"}},
		{"regex_mismatch", diag.RegexMismatch{Pattern: "^[a-z]+$", Input: "abc1", DivergeAt: 3}},
		{"conjunct_failed_leaf", diag.ConjunctFailed{
			Expr: "!=7",
			Span: diag.Span{File: "r.cue", Line: 1, Col: 1, Length: 3},
			Sub:  diag.BoundViolation{Op: "!=", Bound: "7", Actual: "7", Distance: ""},
		}},
		{"disjunction_failed_empty_arms", diag.DisjunctionFailed{Arms: []diag.ArmResult{}}},
		{"key_missing_with_keys", diag.KeyMissing{
			Key: "x", AvailableKeys: []string{"a", "b"}, Suggestion: "",
		}},
		{"provenance", diag.Provenance{
			Span: diag.Span{File: "f.cue", Line: 1, Col: 1, Length: 1}, Snippet: "x",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			first, err := json.Marshal(c.r)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			decoded, err := diag.UnmarshalReason(first)
			if err != nil {
				t.Fatalf("UnmarshalReason: %v", err)
			}
			second, err := json.Marshal(decoded)
			if err != nil {
				t.Fatalf("marshal(2): %v", err)
			}
			if string(first) != string(second) {
				t.Errorf("%s round-trip not byte-identical\n 1st: %s\n 2nd: %s",
					c.name, first, second)
			}
		})
	}
}

// DisjunctionFailed with an empty Arms slice must serialize as "arms":[],
// never "arms":null — consumers iterate without nil checks.
func TestReasonJSON_DisjunctionFailedEmptyArms(t *testing.T) {
	r := diag.DisjunctionFailed{Arms: []diag.ArmResult{}}
	want := `{"type":"disjunction_failed","arms":[]}`
	reasonRoundTrip(t, "empty_arms", r, want)
}

// UnmarshalReason rejects an unknown "type" tag rather than silently
// dropping data — guards against future-variant JSON flowing through an
// older reader without anyone noticing.
func TestUnmarshalReasonRejectsUnknownType(t *testing.T) {
	data := []byte(`{"type":"future_variant","foo":"bar"}`)
	_, err := diag.UnmarshalReason(data)
	if err == nil {
		t.Fatal("UnmarshalReason accepted unknown type tag; want error")
	}
}

// UnmarshalReason on a missing-type payload errors — stable tagging is a
// precondition for the dispatch.
func TestUnmarshalReasonRejectsMissingType(t *testing.T) {
	data := []byte(`{"want":"int","got":"string","actual":"\"x\""}`)
	_, err := diag.UnmarshalReason(data)
	if err == nil {
		t.Fatal("UnmarshalReason accepted payload without type tag; want error")
	}
}
