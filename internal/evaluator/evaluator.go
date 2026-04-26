package evaluator

import (
	"errors"
	"fmt"
	"sync/atomic"

	"cuelang.org/go/cue"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
)

// Match records a rule that matched the enriched input. Action is nil for
// rules whose `then` clause is absent — they contribute to auditability but
// do not emit an effect.
type Match struct {
	Rule   config.Rule
	Action *config.Action
}

// ErrInvalidInput signals that the input value handed to Evaluate is not a
// usable CUE value — engine-level failure, not a rule observation. Callers
// check via errors.Is.
var ErrInvalidInput = errors.New("evaluator: input is not a valid CUE value")

// explainToggle carries the package-level opt-in for debug-path localization.
// Off by default: Subsume-only fast path, zero cost. The CLI flips it on once
// at startup via SetExplainEnabled.
var explainToggle atomic.Bool

// SetExplainEnabled toggles debug-path diagnostic emission for subsequent
// Evaluate calls. When false (the default), non-matching rules skip localize
// entirely and the returned diagnostics slice stays nil.
func SetExplainEnabled(enabled bool) {
	explainToggle.Store(enabled)
}

// explainEnabled reports whether debug-path diagnostic emission is active.
func explainEnabled() bool {
	return explainToggle.Load()
}

// Evaluate checks each rule's `when` clause against input via CUE subsumption
// and returns the matched rules in source order. It returns three orthogonal
// lanes: matches (rules that fired), diagnostics (observations on non-matching
// rules, populated only when explainEnabled()), and error (engine-level
// failures such as invalid input). Engine failures never flow through the
// diagnostics lane.
//
// `cue.Final()` treats input as finalized data so the rule's hidden helper
// fields (`_foo`) are not required to appear in the input; `cue.Schema()`
// treats `when` as an open schema so inputs may carry fields the rule does
// not constrain.
//
// Subsumption does not substitute input values into the pattern, so sibling
// references, `let` bindings, and `if` clauses whose conditions reference
// input-bound siblings do not resolve inside `when`. Rule authors express
// those intents via list patterns or multiple conjuncts instead.
func Evaluate(rules []config.Rule, input cue.Value) ([]Match, []diag.Diagnostic, error) {
	if !input.Exists() || input.Err() != nil {
		return nil, nil, ErrInvalidInput
	}

	matches := make([]Match, 0, len(rules))
	var diags []diag.Diagnostic
	for i, rule := range rules {
		if err := checkWhen(rule, i); err != nil {
			return nil, nil, err
		}

		if rule.When.Subsume(input, cue.Final(), cue.Schema()) == nil {
			matches = append(matches, Match{Rule: rule, Action: rule.Then})
			continue
		}

		if !explainEnabled() {
			continue
		}
		for d := range localize(rule, input) {
			diags = append(diags, d)
		}
	}
	return matches, diags, nil
}

// checkWhen rejects rules whose `when` clause is a scalar, bottom, or otherwise
// structurally invalid before unification — those conditions indicate a
// malformed rule the loader failed to catch, and silently skipping them would
// mask policy bugs.
func checkWhen(rule config.Rule, index int) error {
	when := rule.When
	if err := when.Err(); err != nil {
		return fmt.Errorf("rule %q (index %d): when clause is malformed: %w", rule.Source, index, err)
	}
	if k := when.IncompleteKind(); k&cue.StructKind == 0 {
		return fmt.Errorf("rule %q (index %d): when clause must be a struct, got %s", rule.Source, index, k)
	}
	return nil
}
