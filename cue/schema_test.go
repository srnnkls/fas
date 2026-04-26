// Package cue_test asserts that the shipped schema.cue file compiles and
// unifies correctly with representative fixtures. Schema bytes come from
// the exported SchemaSource() helper so the //go:embed wiring is exercised
// — an empty embed directive would fail these tests.
package cue_test

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	fascue "github.com/srnnkls/fas/cue"
)

// compileSchema returns the compiled #Rule / #Input / #Action etc.
// namespace from the embedded schema.cue.
func compileSchema(t *testing.T) (*cue.Context, cue.Value) {
	t.Helper()
	src := fascue.SchemaSource()
	if len(src) == 0 {
		t.Fatal("SchemaSource() returned empty bytes; //go:embed wiring is broken")
	}
	ctx := cuecontext.New()
	v := ctx.CompileBytes(src)
	if err := v.Err(); err != nil {
		t.Fatalf("compile schema.cue: %v", err)
	}
	return ctx, v
}

func TestSchema_CompilesCleanly(t *testing.T) {
	_, _ = compileSchema(t)
}

func TestSchema_InputAcceptsValidFixture(t *testing.T) {
	ctx, schema := compileSchema(t)
	input := schema.LookupPath(cue.ParsePath("#Input"))
	if err := input.Err(); err != nil {
		t.Fatalf("lookup #Input: %v", err)
	}

	fixture := ctx.CompileString(`{
		hook_event_name: "PreToolUse"
		tool_name: "Bash"
		tool_input: {command: "ls"}
	}`)
	if err := fixture.Err(); err != nil {
		t.Fatalf("compile fixture: %v", err)
	}

	unified := input.Unify(fixture)
	if err := unified.Validate(cue.Concrete(true)); err != nil {
		t.Fatalf("valid input failed #Input unification: %v", err)
	}
}

func TestSchema_InputRejectsMissingRequiredField(t *testing.T) {
	ctx, schema := compileSchema(t)
	input := schema.LookupPath(cue.ParsePath("#Input"))
	if err := input.Err(); err != nil {
		t.Fatalf("lookup #Input: %v", err)
	}

	// hook_event_name is required — omitting it must fail concreteness.
	fixture := ctx.CompileString(`{tool_name: "Bash"}`)
	if err := fixture.Err(); err != nil {
		t.Fatalf("compile fixture: %v", err)
	}
	unified := input.Unify(fixture)
	err := unified.Validate(cue.Concrete(true))
	if err == nil {
		t.Fatalf("expected validation error for missing hook_event_name")
	}
	if !strings.Contains(err.Error(), "hook_event_name") {
		t.Fatalf("error should mention hook_event_name, got: %v", err)
	}
}

func TestSchema_DenyActionDefaultsSeverity(t *testing.T) {
	ctx, schema := compileSchema(t)
	deny := schema.LookupPath(cue.ParsePath("#Deny"))
	if err := deny.Err(); err != nil {
		t.Fatalf("lookup #Deny: %v", err)
	}

	fixture := ctx.CompileString(`{deny: {rule_id: "r1", reason: "nope"}}`)
	if err := fixture.Err(); err != nil {
		t.Fatalf("compile fixture: %v", err)
	}
	unified := deny.Unify(fixture)
	if err := unified.Validate(); err != nil {
		t.Fatalf("default-severity Deny failed validation: %v", err)
	}
	sev := unified.LookupPath(cue.ParsePath("deny.severity"))
	if err := sev.Err(); err != nil {
		t.Fatalf("lookup deny.severity: %v", err)
	}
	defaulted, _ := sev.Default()
	s, err := defaulted.String()
	if err != nil {
		t.Fatalf("read deny.severity default: %v", err)
	}
	if s != "HIGH" {
		t.Fatalf("expected default severity HIGH, got %q", s)
	}
}

func TestSchema_NoHaltOrBlockDefinition(t *testing.T) {
	_, schema := compileSchema(t)
	for _, name := range []string{"#Halt", "#Block"} {
		v := schema.LookupPath(cue.ParsePath(name))
		if v.Exists() {
			t.Errorf("schema must not define %s (collapsed into #Deny)", name)
		}
	}
}

func TestSchema_RuleAcceptsDenyAction(t *testing.T) {
	ctx, schema := compileSchema(t)
	rule := schema.LookupPath(cue.ParsePath("#Rule"))
	if err := rule.Err(); err != nil {
		t.Fatalf("lookup #Rule: %v", err)
	}
	fixture := ctx.CompileString(`{
		when: {hook_event_name: "PreToolUse"}
		then: deny: {rule_id: "r1", reason: "nope"}
	}`)
	if err := fixture.Err(); err != nil {
		t.Fatalf("compile rule fixture: %v", err)
	}
	unified := rule.Unify(fixture)
	if err := unified.Validate(); err != nil {
		t.Fatalf("valid rule failed #Rule unification: %v", err)
	}
}

func TestSchema_RuleRejectsUnknownGate(t *testing.T) {
	ctx, schema := compileSchema(t)
	rule := schema.LookupPath(cue.ParsePath("#Rule"))
	if err := rule.Err(); err != nil {
		t.Fatalf("lookup #Rule: %v", err)
	}
	// `halt` is not a member of #Action. This must fail closed-set
	// validation.
	fixture := ctx.CompileString(`{
		when: {hook_event_name: "PreToolUse"}
		then: halt: {rule_id: "r1", reason: "stop"}
	}`)
	if err := fixture.Err(); err != nil {
		t.Fatalf("compile rule fixture: %v", err)
	}
	unified := rule.Unify(fixture)
	if err := unified.Validate(cue.Concrete(true)); err == nil {
		t.Fatalf("expected error unifying rule with unknown gate 'halt'")
	}
}
