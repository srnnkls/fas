package evaluator

import (
	"fmt"

	"cuelang.org/go/cue"

	"github.com/srnnkls/quae/internal/config"
)

// Match records a rule that matched the enriched input. Action is nil for
// rules whose `then` clause is absent — they contribute to auditability but
// do not emit an effect.
type Match struct {
	Rule   config.Rule
	Action *config.Action
}

// Evaluate unifies each rule's `when` clause against input and returns the
// matched rules in source order.
func Evaluate(rules []config.Rule, input cue.Value) ([]Match, error) {
	matches := make([]Match, 0, len(rules))
	for i, rule := range rules {
		if err := checkWhen(rule, i); err != nil {
			return nil, err
		}

		if !inputSatisfies(rule.When, input) {
			continue
		}

		matches = append(matches, Match{Rule: rule, Action: rule.Then})
	}
	return matches, nil
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

// inputSatisfies reports whether input meets every constraint in when. A
// struct when requires every field to be satisfied at the matching input path;
// unification alone is insufficient because CUE happily fills missing input
// fields with the constraint's concrete value, masking unsatisfied rules.
func inputSatisfies(when, input cue.Value) bool {
	if when.IncompleteKind()&cue.StructKind != 0 {
		iter, err := when.Fields(cue.All())
		if err != nil {
			return false
		}
		for iter.Next() {
			sel := iter.Selector()
			sub := input.LookupPath(cue.MakePath(sel))
			if !sub.Exists() {
				return false
			}
			if !inputSatisfies(iter.Value(), sub) {
				return false
			}
		}
		return true
	}

	unified := when.Unify(input)
	if unified.Err() != nil {
		return false
	}
	return unified.Validate(cue.Concrete(false)) == nil
}
