package diag_test

import (
	"reflect"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"

	"github.com/srnnkls/quae/internal/diag"
)

// Each concrete Reason variant can be assigned to a diag.Reason variable.
// The assignment is itself the assertion — if any variant stops implementing
// the sealed interface (e.g. the unexported reason() method is removed or
// renamed), this file fails to compile. Compile errors are the RED state for
// this test.
func TestEachVariantImplementsReason(t *testing.T) {
	var _ diag.Reason = diag.KindMismatch{}
	var _ diag.Reason = diag.BoundViolation{}
	var _ diag.Reason = diag.RegexMismatch{}
	var _ diag.Reason = diag.ConjunctFailed{}
	var _ diag.Reason = diag.DisjunctionFailed{}
	var _ diag.Reason = diag.KeyMissing{}
	var _ diag.Reason = diag.Provenance{}

	// If we got here at compile time, the seal holds. At runtime record the
	// fact — the body can't be empty without go vet complaining.
	t.Log("sealed interface satisfied by all 7 variants")
}

// Label gains a Reasons field whose element type is diag.Reason, and whose
// zero length is the v0 fallthrough. Msg is still present.
func TestLabelHasReasonsSliceField(t *testing.T) {
	lt := reflect.TypeFor[diag.Label]()

	f, ok := lt.FieldByName("Reasons")
	if !ok {
		t.Fatal("diag.Label: no field named Reasons")
	}
	if f.Type.Kind() != reflect.Slice {
		t.Fatalf("diag.Label.Reasons: kind = %s, want Slice", f.Type.Kind())
	}
	reasonIface := reflect.TypeFor[diag.Reason]()
	if f.Type.Elem() != reasonIface {
		t.Fatalf("diag.Label.Reasons element type = %s, want diag.Reason", f.Type.Elem())
	}

	// Msg must remain — NF5 backwards compat.
	if _, ok := lt.FieldByName("Msg"); !ok {
		t.Error("diag.Label: Msg field must still exist (NF5 fallthrough)")
	}

	// Zero-value Label has nil Reasons and empty Msg.
	var zero diag.Label
	if zero.Reasons != nil {
		t.Errorf("zero Label.Reasons = %v, want nil", zero.Reasons)
	}
	if zero.Msg != "" {
		t.Errorf("zero Label.Msg = %q, want empty", zero.Msg)
	}
}

// No Reason variant field (and no field on Span or ArmResult) holds an
// ast.Expr, *ast.X, token.Pos, or cue.Value. These would break round-trip
// determinism — AST/position data must be pre-resolved into DTO strings
// and Span structs before entering the Reason model.
func TestNoRawASTOrCUETypesInReasonModel(t *testing.T) {
	// Forbidden types (and the pointer to their addressable forms). We do
	// not enumerate every *ast.X concrete type — we walk the tree and reject
	// anything whose PkgPath is cuelang.org/go/cue/ast or cuelang.org/go/cue
	// (the latter covers cue.Value). token.Pos and its pointer are named
	// explicitly since they live in cuelang.org/go/cue/token.
	forbiddenPkgs := map[string]struct{}{
		"cuelang.org/go/cue/ast":   {},
		"cuelang.org/go/cue":       {},
		"cuelang.org/go/cue/token": {},
	}
	// ...but cue.Kind is an OK leaf — it's an int alias, the JSON layer
	// stringifies it. So we allow specific names even when their package
	// is in the forbidden list.
	allowed := map[[2]string]struct{}{
		{"cuelang.org/go/cue", "Kind"}: {},
	}

	types := []reflect.Type{
		reflect.TypeFor[diag.Span](),
		reflect.TypeFor[diag.KindMismatch](),
		reflect.TypeFor[diag.BoundViolation](),
		reflect.TypeFor[diag.RegexMismatch](),
		reflect.TypeFor[diag.ConjunctFailed](),
		reflect.TypeFor[diag.DisjunctionFailed](),
		reflect.TypeFor[diag.KeyMissing](),
		reflect.TypeFor[diag.Provenance](),
		reflect.TypeFor[diag.ArmResult](),
	}

	for _, ty := range types {
		walkFields(t, ty, ty.Name(), forbiddenPkgs, allowed)
	}

	// Spot-check: the forbidden list actually matches something — if this
	// fails the test itself is vacuous.
	if reflect.TypeFor[ast.Expr]().PkgPath() != "cuelang.org/go/cue/ast" {
		t.Fatal("ast.Expr not in expected package; adjust forbiddenPkgs")
	}
	if reflect.TypeFor[token.Pos]().PkgPath() != "cuelang.org/go/cue/token" {
		t.Fatal("token.Pos not in expected package; adjust forbiddenPkgs")
	}
	if reflect.TypeFor[cue.Value]().PkgPath() != "cuelang.org/go/cue" {
		t.Fatal("cue.Value not in expected package; adjust forbiddenPkgs")
	}
}

// walkFields descends into nested struct types, reporting any field whose
// type (after dereferencing pointers / unwrapping slices+arrays+maps) lives
// in one of the forbidden packages, unless that specific (pkg,name) pair
// was explicitly allowed.
//
// The Reason interface itself is allowed wherever it appears (Sub, Inner):
// it's the seal, not a raw type.
func walkFields(
	t *testing.T,
	ty reflect.Type,
	path string,
	forbiddenPkgs map[string]struct{},
	allowed map[[2]string]struct{},
) {
	t.Helper()

	reasonIface := reflect.TypeFor[diag.Reason]()

	if ty.Kind() != reflect.Struct {
		return
	}
	for f := range ty.Fields() {
		if !f.IsExported() {
			continue
		}
		fp := path + "." + f.Name
		ft := f.Type

		// Reason interface fields (Sub, Inner) are the seal — allowed.
		if ft == reasonIface {
			continue
		}

		checkType(t, ft, fp, forbiddenPkgs, allowed, reasonIface)
	}
}

func checkType(
	t *testing.T,
	ft reflect.Type,
	fp string,
	forbiddenPkgs map[string]struct{},
	allowed map[[2]string]struct{},
	reasonIface reflect.Type,
) {
	t.Helper()

	// Unwrap pointer / slice / array / map layers.
	for {
		switch ft.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			ft = ft.Elem()
			continue
		case reflect.Map:
			ft = ft.Elem()
			continue
		}
		break
	}

	// Reason interface inside a container (e.g. []Reason on Label) is fine.
	if ft == reasonIface {
		return
	}

	pkg := ft.PkgPath()
	name := ft.Name()

	if _, bad := forbiddenPkgs[pkg]; bad {
		if _, ok := allowed[[2]string{pkg, name}]; !ok {
			t.Errorf("%s: field type %s.%s is forbidden in Reason model (pre-resolve to DTO before construction)",
				fp, pkg, name)
			return
		}
	}

	// Descend into nested structs that aren't in forbidden packages.
	if ft.Kind() == reflect.Struct && pkg != "" {
		// Avoid infinite recursion on self-referential types: bail once we
		// leave our own package. DTOs are allowed to nest into diag.Span /
		// diag.ArmResult, which live in the package under test.
		if !strings.HasSuffix(pkg, "/internal/diag") {
			return
		}
	}
	if ft.Kind() == reflect.Struct {
		for f := range ft.Fields() {
			if !f.IsExported() {
				continue
			}
			checkType(t, f.Type, fp+"."+f.Name, forbiddenPkgs, allowed, reasonIface)
		}
	}
}

// Invariant I3a: multiplicity lives on Label.Reasons, never inside a
// singular variant. No variant may hold []diag.Reason. Slices of other
// DTOs (DisjunctionFailed.Arms []ArmResult, KeyMissing.AvailableKeys
// []string) are permitted — the rule is specifically about []Reason.
func TestNoVariantHasReasonSliceField(t *testing.T) {
	reasonSlice := reflect.TypeFor[[]diag.Reason]()

	variants := []reflect.Type{
		reflect.TypeFor[diag.KindMismatch](),
		reflect.TypeFor[diag.BoundViolation](),
		reflect.TypeFor[diag.RegexMismatch](),
		reflect.TypeFor[diag.ConjunctFailed](),
		reflect.TypeFor[diag.DisjunctionFailed](),
		reflect.TypeFor[diag.KeyMissing](),
		reflect.TypeFor[diag.Provenance](),
		// ArmResult isn't a Reason variant itself but lives inside one;
		// enforce the same rule.
		reflect.TypeFor[diag.ArmResult](),
	}
	for _, v := range variants {
		for f := range v.Fields() {
			if f.Type == reasonSlice {
				t.Errorf("%s.%s: has type []diag.Reason — I3a violation, multiplicity belongs on Label",
					v.Name(), f.Name)
			}
		}
	}
}

// ConjunctFailed.Sub is a Reason, so it must be able to nest another
// ConjunctFailed without any structural limit. Build a 5-deep chain and
// confirm construction, assignment to Reason, and field access all survive.
// (Renderer-level recursion limits, if any, are a T12 concern, not T1.)
func TestConjunctFailedSubNestsWithoutStackOverflow(t *testing.T) {
	inner := diag.Reason(diag.BoundViolation{Op: ">=", Bound: "0", Actual: "-5", Distance: "off by 5"})

	// Build 5 levels of ConjunctFailed wrapping the innermost BoundViolation.
	cur := inner
	for i := range 5 {
		cur = diag.ConjunctFailed{
			Expr: "wrap_" + strings.Repeat("x", i),
			Span: diag.Span{File: "r.cue", Line: 10 + i, Col: 1, Length: 3},
			Sub:  cur,
		}
	}

	// Walk the chain, confirm the innermost is still the BoundViolation.
	depth := 0
	for {
		cf, ok := cur.(diag.ConjunctFailed)
		if !ok {
			break
		}
		depth++
		cur = cf.Sub
	}
	if depth != 5 {
		t.Errorf("nested ConjunctFailed depth = %d, want 5", depth)
	}
	bv, ok := cur.(diag.BoundViolation)
	if !ok {
		t.Fatalf("innermost Sub = %T, want diag.BoundViolation", cur)
	}
	if bv.Distance != "off by 5" {
		t.Errorf("innermost BoundViolation.Distance = %q, want %q", bv.Distance, "off by 5")
	}
}

// Label with Reasons=[a,b,c] holds exactly the slice given, in order. Guards
// that the field is a plain slice and not sorted/dedup'd/reversed by any
// accidental processing.
func TestLabelReasonsPreservesOrderAndLength(t *testing.T) {
	a := diag.KindMismatch{Want: cue.IntKind, Got: cue.StringKind, Actual: `"x"`}
	b := diag.BoundViolation{Op: ">=", Bound: "5", Actual: "3", Distance: "off by 2"}
	c := diag.KeyMissing{Key: "foo", AvailableKeys: []string{"bar"}, Suggestion: "bar"}

	l := diag.Label{
		Pos:     token.NoPos,
		Len:     3,
		Msg:     "should not be consulted when Reasons is non-empty",
		Reasons: []diag.Reason{a, b, c},
	}
	if got := len(l.Reasons); got != 3 {
		t.Fatalf("Label.Reasons length = %d, want 3", got)
	}
	if _, ok := l.Reasons[0].(diag.KindMismatch); !ok {
		t.Errorf("Reasons[0] type = %T, want diag.KindMismatch", l.Reasons[0])
	}
	if _, ok := l.Reasons[1].(diag.BoundViolation); !ok {
		t.Errorf("Reasons[1] type = %T, want diag.BoundViolation", l.Reasons[1])
	}
	if _, ok := l.Reasons[2].(diag.KeyMissing); !ok {
		t.Errorf("Reasons[2] type = %T, want diag.KeyMissing", l.Reasons[2])
	}
}

// The Span DTO is a value type with exactly four fields: File, Line, Col,
// Length. Pinning the shape guards against silent additions that would
// invalidate the JSON schema consumers build against.
func TestSpanShape(t *testing.T) {
	st := reflect.TypeFor[diag.Span]()

	wantFields := map[string]reflect.Kind{
		"File":   reflect.String,
		"Line":   reflect.Int,
		"Col":    reflect.Int,
		"Length": reflect.Int,
	}
	if st.NumField() != len(wantFields) {
		t.Fatalf("diag.Span has %d fields, want %d", st.NumField(), len(wantFields))
	}
	for name, kind := range wantFields {
		f, ok := st.FieldByName(name)
		if !ok {
			t.Errorf("diag.Span: missing field %s", name)
			continue
		}
		if f.Type.Kind() != kind {
			t.Errorf("diag.Span.%s kind = %s, want %s", name, f.Type.Kind(), kind)
		}
	}
}
