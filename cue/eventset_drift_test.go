package cue_test

import (
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	fascue "github.com/srnnkls/fas/cue"
)

// disjunctionStrings returns the string members of a disjunction; a
// single-member value has no OrOp and contributes its own string.
func disjunctionStrings(t *testing.T, v cue.Value) []string {
	t.Helper()
	op, operands := v.Expr()
	if op != cue.OrOp {
		s, err := v.String()
		if err != nil {
			t.Fatalf("disjunction member not a string: %v", err)
		}
		return []string{s}
	}
	out := make([]string, 0, len(operands))
	for _, o := range operands {
		s, err := o.String()
		if err != nil {
			t.Fatalf("disjunction operand not a string: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func structFieldValues(t *testing.T, v cue.Value) []string {
	t.Helper()
	iter, err := v.Fields(cue.Definitions(false), cue.Hidden(false))
	if err != nil {
		t.Fatalf("not a struct: %v", err)
	}
	var out []string
	for iter.Next() {
		s, err := iter.Value().String()
		if err != nil {
			t.Fatalf("field %s not a string: %v", iter.Selector(), err)
		}
		out = append(out, s)
	}
	return out
}

func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

func schemaEventNames(t *testing.T) []string {
	t.Helper()
	ctx := cuecontext.New()
	v := ctx.CompileBytes(fascue.SchemaSource())
	if err := v.Err(); err != nil {
		t.Fatalf("compile schema.cue: %v", err)
	}
	def := v.LookupPath(cue.MakePath(cue.Def("HookEventName")))
	if err := def.Err(); err != nil {
		t.Fatalf("lookup schema #HookEventName: %v", err)
	}
	return disjunctionStrings(t, def)
}

func catalogEventNames(t *testing.T, ctx *cue.Context) []string {
	t.Helper()
	pkg := loadSubPkg(t, ctx, subPkgCatalog)
	def := lookupDef(t, pkg, "EventName")
	return structFieldValues(t, def)
}

func hookEventNames(t *testing.T, ctx *cue.Context) []string {
	t.Helper()
	pkg := loadSubPkg(t, ctx, subPkgHook)
	def := lookupDef(t, pkg, "HookEventName")
	return disjunctionStrings(t, def)
}

func symmetricDiff(a, b map[string]bool) []string {
	var diff []string
	for n := range a {
		if !b[n] {
			diff = append(diff, n)
		}
	}
	for n := range b {
		if !a[n] {
			diff = append(diff, n)
		}
	}
	sort.Strings(diff)
	return diff
}

// TestEventSet_NoDriftAcrossSources fails if the hook-event-name set diverges
// across schema.cue, catalog.#EventName, and hook.#HookEventName.
func TestEventSet_NoDriftAcrossSources(t *testing.T) {
	ctx := cuecontext.New()

	schema := schemaEventNames(t)
	catalog := catalogEventNames(t, ctx)
	hook := hookEventNames(t, ctx)

	const wantCount = 7
	for _, src := range []struct {
		name  string
		names []string
	}{
		{"schema", schema},
		{"catalog", catalog},
		{"hook", hook},
	} {
		if len(src.names) != wantCount {
			t.Fatalf("%s enumeration returned %d names %v, want %d",
				src.name, len(src.names), src.names, wantCount)
		}
	}

	schemaSet := toSet(schema)
	catalogSet := toSet(catalog)
	hookSet := toSet(hook)

	for _, pair := range []struct {
		aName, bName string
		a, b         map[string]bool
	}{
		{"schema", "catalog", schemaSet, catalogSet},
		{"catalog", "hook", catalogSet, hookSet},
		{"schema", "hook", schemaSet, hookSet},
	} {
		if diff := symmetricDiff(pair.a, pair.b); len(diff) > 0 {
			t.Errorf("event-name drift between %s and %s: %s differ",
				pair.aName, pair.bName, strings.Join(diff, ", "))
		}
	}
}
