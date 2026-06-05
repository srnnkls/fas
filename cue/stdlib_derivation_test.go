package cue_test

import (
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func catalogMembers(t *testing.T, pkg cue.Value, def string) map[string]string {
	t.Helper()
	v := lookupDef(t, pkg, def)
	iter, err := v.Fields(cue.Definitions(false), cue.Hidden(false))
	if err != nil {
		t.Fatalf("catalog.#%s not a struct: %v", def, err)
	}
	out := map[string]string{}
	for iter.Next() {
		s, err := iter.Value().String()
		if err != nil {
			t.Fatalf("catalog.#%s.%s not a string: %v", def, iter.Selector(), err)
		}
		out[iter.Selector().String()] = s
	}
	return out
}

func binderBindings(t *testing.T, pkg cue.Value, def, wireField string) map[string]string {
	t.Helper()
	v := lookupDef(t, pkg, def)
	iter, err := v.Fields(cue.Definitions(false), cue.Hidden(false))
	if err != nil {
		t.Fatalf("%s not a struct: %v", def, err)
	}
	out := map[string]string{}
	for iter.Next() {
		member := iter.Selector().String()
		wire := iter.Value().LookupPath(cue.ParsePath(wireField))
		s, err := wire.String()
		if err != nil {
			t.Fatalf("%s.%s.%s not a concrete string: %v", def, member, wireField, err)
		}
		out[member] = s
	}
	return out
}

func keySet(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// assertBijection: each catalog member k→v has a binder member k binding the
// wire value v, and the binder carries no member the catalog lacks.
func assertBijection(t *testing.T, axis string, catalog, binder map[string]string) {
	t.Helper()

	if diff := symmetricDiff(keySet(catalog), keySet(binder)); len(diff) > 0 {
		t.Errorf("%s: member-set drift between catalog and binder: %s differ",
			axis, strings.Join(diff, ", "))
	}

	keys := make([]string, 0, len(catalog))
	for k := range catalog {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		bound, ok := binder[k]
		if !ok {
			t.Errorf("%s: catalog member %q has no binder member", axis, k)
			continue
		}
		if bound != catalog[k] {
			t.Errorf("%s: binder member %q binds %q, catalog pins %q",
				axis, k, bound, catalog[k])
		}
	}
}

// TestDerivation_CatalogBinderBijection: each binder member equals exactly one
// catalog member by key AND by the wire value it pins; drop or mis-bind one and
// this turns red even if the key set still matches.
func TestDerivation_CatalogBinderBijection(t *testing.T) {
	ctx := cuecontext.New()
	catalogPkg := loadSubPkg(t, ctx, subPkgCatalog)
	toolPkg := loadSubPkg(t, ctx, subPkgTool)
	hookPkg := loadSubPkg(t, ctx, subPkgHook)

	assertBijection(t, "ToolName↔#Tool",
		catalogMembers(t, catalogPkg, "ToolName"),
		binderBindings(t, toolPkg, "Tool", "tool_name"))

	assertBijection(t, "AgentType↔#Agent",
		catalogMembers(t, catalogPkg, "AgentType"),
		binderBindings(t, hookPkg, "Agent", "agent_type"))
}
