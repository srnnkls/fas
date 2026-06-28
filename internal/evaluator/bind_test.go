package evaluator_test

import (
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/evaluator"
)

// TestEvaluate_Bind_EqualFieldsMatch verifies that two fields annotated with
// the same @bind variable match when the input carries equal values at both
// paths.
func TestEvaluate_Bind_EqualFieldsMatch(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "bind_eq.cue", `{
		when: {
			a: string @bind(X)
			b: string @bind(X)
		}
		then: deny: {rule_id: "bind-eq", reason: "a equals b"}
	}`)
	rules := loadRules(t, dir)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if len(rules[0].Bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(rules[0].Bindings))
	}

	ctx := cuecontext.New()

	t.Run("equal values match", func(t *testing.T) {
		input := mustCompile(t, ctx, `{a: "hello", b: "hello"}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d", len(got))
		}
		if got[0].Action.RuleID != "bind-eq" {
			t.Fatalf("expected rule_id=bind-eq, got %q", got[0].Action.RuleID)
		}
	})

	t.Run("different values do not match", func(t *testing.T) {
		input := mustCompile(t, ctx, `{a: "hello", b: "world"}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches, got %d", len(got))
		}
	})
}

// TestEvaluate_Bind_SubIndex verifies that @bind with a sub-path index
// resolves to the correct list element.
func TestEvaluate_Bind_SubIndex(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "bind_idx.cue", `{
		when: {
			tool_input: {
				command: string @bind(X)
				parsed: targets: [...string] @bind(X, 0)
			}
		}
		then: deny: {rule_id: "bind-idx", reason: "command equals first target"}
	}`)
	rules := loadRules(t, dir)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if len(rules[0].Bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(rules[0].Bindings))
	}

	ctx := cuecontext.New()

	t.Run("command equals targets[0]", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			tool_input: {
				command: "cat"
				parsed: targets: ["cat", "/etc/passwd"]
			}
		}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d", len(got))
		}
	})

	t.Run("command differs from targets[0]", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			tool_input: {
				command: "rm"
				parsed: targets: ["cat", "/etc/passwd"]
			}
		}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches, got %d", len(got))
		}
	})
}

// TestEvaluate_Bind_StaticConstraintStillApplies confirms that Subsume checks
// the static part of the pattern (e.g., string type constraint) before
// bindings are evaluated. If the static part doesn't match, the binding
// check is never reached.
func TestEvaluate_Bind_StaticConstraintStillApplies(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "bind_static.cue", `{
		when: {
			a: =~"^hello" @bind(X)
			b: string @bind(X)
		}
		then: deny: {rule_id: "bind-static", reason: "a starts with hello and equals b"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()

	t.Run("both static and binding satisfied", func(t *testing.T) {
		input := mustCompile(t, ctx, `{a: "hello world", b: "hello world"}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d", len(got))
		}
	})

	t.Run("static fails (regex), binding would pass", func(t *testing.T) {
		input := mustCompile(t, ctx, `{a: "goodbye", b: "goodbye"}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches (regex fails), got %d", len(got))
		}
	})

	t.Run("static passes, binding fails", func(t *testing.T) {
		input := mustCompile(t, ctx, `{a: "hello world", b: "hello earth"}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches (binding fails), got %d", len(got))
		}
	})
}

// TestEvaluate_Bind_SingleVariable_NoConstraint confirms that a variable
// referenced only once adds no constraint — it's a capture, not an equality.
func TestEvaluate_Bind_SingleVariable_NoConstraint(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "bind_single.cue", `{
		when: {
			a: string @bind(X)
		}
		then: deny: {rule_id: "bind-single", reason: "just a capture"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()
	input := mustCompile(t, ctx, `{a: "anything"}`)

	got, _, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("single-variable binding should not constrain; expected 1 match, got %d", len(got))
	}
}

// TestEvaluate_Bind_MultipleVariables confirms that multiple independent
// variable groups are checked independently.
func TestEvaluate_Bind_MultipleVariables(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "bind_multi.cue", `{
		when: {
			a: string @bind(X)
			b: string @bind(X)
			c: string @bind(Y)
			d: string @bind(Y)
		}
		then: deny: {rule_id: "bind-multi", reason: "two pairs"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()

	t.Run("both pairs equal", func(t *testing.T) {
		input := mustCompile(t, ctx, `{a: "foo", b: "foo", c: "bar", d: "bar"}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d", len(got))
		}
	})

	t.Run("first pair differs", func(t *testing.T) {
		input := mustCompile(t, ctx, `{a: "foo", b: "baz", c: "bar", d: "bar"}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches, got %d", len(got))
		}
	})

	t.Run("second pair differs", func(t *testing.T) {
		input := mustCompile(t, ctx, `{a: "foo", b: "foo", c: "bar", d: "qux"}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches, got %d", len(got))
		}
	})
}

// TestEvaluate_Bind_ExplainEnabled_BindingFailureEmitsDiagnostic confirms
// that a binding mismatch emits an E0601 diagnostic when explain is enabled.
func TestEvaluate_Bind_ExplainEnabled_BindingFailureEmitsDiagnostic(t *testing.T) {
	evaluator.SetExplainEnabled(true)
	t.Cleanup(func() { evaluator.SetExplainEnabled(false) })

	dir := t.TempDir()
	mustWriteRule(t, dir, "bind_diag.cue", `{
		when: {
			a: string @bind(X)
			b: string @bind(X)
		}
		then: deny: {rule_id: "bind-diag", reason: "a equals b"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()
	input := mustCompile(t, ctx, `{a: "hello", b: "world"}`)

	got, diags, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(got))
	}
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic for binding failure, got none")
	}
	if diags[0].Code != "E0601" {
		t.Errorf("diagnostic code = %q, want E0601", diags[0].Code)
	}
}

// TestEvaluate_Bind_NoBindings_RuleUnchanged confirms that rules without
// @bind attributes behave exactly as before.
func TestEvaluate_Bind_NoBindings_RuleUnchanged(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "no_bind.cue", `{
		when: {tool_name: "Bash"}
		then: deny: {rule_id: "no-bind", reason: "bash"}
	}`)
	rules := loadRules(t, dir)
	if len(rules[0].Bindings) != 0 {
		t.Fatalf("expected 0 bindings on rule without @bind, got %d", len(rules[0].Bindings))
	}

	ctx := cuecontext.New()
	input := mustCompile(t, ctx, `{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	got, _, err := evaluator.Evaluate(rules, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d", len(got))
	}
}

// TestEvaluate_Bind_NestedStructPath confirms that @bind works on fields
// nested deep in the when struct.
func TestEvaluate_Bind_NestedStructPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteRule(t, dir, "bind_nested.cue", `{
		when: {
			tool_input: {
				command: string @bind(X)
				parsed: {
					subcommands: [...string] @bind(X, 0)
				}
			}
		}
		then: deny: {rule_id: "bind-nested", reason: "command equals first subcommand"}
	}`)
	rules := loadRules(t, dir)

	ctx := cuecontext.New()

	t.Run("equal", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			tool_input: {
				command: "git"
				parsed: subcommands: ["git", "push"]
			}
		}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d", len(got))
		}
	})

	t.Run("different", func(t *testing.T) {
		input := mustCompile(t, ctx, `{
			tool_input: {
				command: "git"
				parsed: subcommands: ["svn", "push"]
			}
		}`)
		got, _, err := evaluator.Evaluate(rules, input)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 matches, got %d", len(got))
		}
	})
}
