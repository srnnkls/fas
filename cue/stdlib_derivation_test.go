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

// assertTriad: independent expected oracle ↔ catalog ↔ binder must agree on the
// member set (both directions) AND on the wire value each member pins.
func assertTriad(t *testing.T, axis string, expected, catalog, binder map[string]string) {
	t.Helper()

	if diff := symmetricDiff(keySet(expected), keySet(catalog)); len(diff) > 0 {
		t.Errorf("%s: member-set drift between expected oracle and catalog: %s differ",
			axis, strings.Join(diff, ", "))
	}
	if diff := symmetricDiff(keySet(expected), keySet(binder)); len(diff) > 0 {
		t.Errorf("%s: member-set drift between expected oracle and binder: %s differ",
			axis, strings.Join(diff, ", "))
	}

	keys := make([]string, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		want := expected[k]
		if got, ok := catalog[k]; !ok {
			t.Errorf("%s: expected member %q absent from catalog", axis, k)
		} else if got != want {
			t.Errorf("%s: catalog member %q pins %q, expected %q", axis, k, got, want)
		}
		if got, ok := binder[k]; !ok {
			t.Errorf("%s: expected member %q has no binder member", axis, k)
		} else if got != want {
			t.Errorf("%s: binder member %q binds %q, expected %q", axis, k, got, want)
		}
	}
}

// TestDerivation_CatalogBinderBijection: the binder is a CUE comprehension over
// the catalog, so the catalog alone cannot be the oracle — these expected maps
// are the independent oracle, so adding/removing a tool or agent (or mis-binding
// a wire value) requires an intentional edit here and cannot silently pass.
func TestDerivation_CatalogBinderBijection(t *testing.T) {
	wantTools := map[string]string{
		"Agent":           "Agent",
		"AskUserQuestion": "AskUserQuestion",
		"Bash":            "Bash",
		"Edit":            "Edit",
		"Glob":            "Glob",
		"Grep":            "Grep",
		"NotebookEdit":    "NotebookEdit",
		"Read":            "Read",
		"TaskCreate":      "TaskCreate",
		"TaskGet":         "TaskGet",
		"TaskList":        "TaskList",
		"TaskUpdate":      "TaskUpdate",
		"TaskStop":        "TaskStop",
		"TodoWrite":       "TodoWrite",
		"WebFetch":        "WebFetch",
		"WebSearch":       "WebSearch",
		"Write":           "Write",
	}
	wantAgents := map[string]string{
		"Explore":        "Explore",
		"Plan":           "Plan",
		"GeneralPurpose": "general-purpose",
	}

	ctx := cuecontext.New()
	catalogPkg := loadSubPkg(t, ctx, subPkgCatalog)
	toolPkg := loadSubPkg(t, ctx, subPkgTool)
	hookPkg := loadSubPkg(t, ctx, subPkgHook)

	assertTriad(t, "ToolName↔#Tool", wantTools,
		catalogMembers(t, catalogPkg, "ToolName"),
		binderBindings(t, toolPkg, "Tool", "tool_name"))

	assertTriad(t, "AgentType↔#Agent", wantAgents,
		catalogMembers(t, catalogPkg, "AgentType"),
		binderBindings(t, hookPkg, "Agent", "agent_type"))
}
