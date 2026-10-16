package parser

import (
	"slices"
	"testing"
)

func TestParseBash_NestingLimitIsUnresolved(t *testing.T) {
	got := parseBash(`sh -c "rm -rf x"`, maxNestedScripts)
	reasons, _ := got.Attributes[UnresolvedScript].([]string)
	if len(reasons) == 0 {
		t.Fatalf("attributes %+v missing %s at the nesting limit", got.Attributes, UnresolvedScript)
	}
	if slices.ContainsFunc(got.Calls, func(c Call) bool { return c.Command == "rm" }) {
		t.Errorf("script past the nesting limit was parsed: %+v", got.Calls)
	}
}
