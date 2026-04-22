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

// Evaluate checks each rule's `when` clause against input via CUE subsumption
// and returns the matched rules in source order. `cue.Final()` treats input
// as finalized data so the rule's hidden helper fields (`_foo`) are not
// required to appear in the input; `cue.Schema()` treats `when` as an open
// schema so inputs may carry fields the rule does not constrain (e.g. the
// Bash parser's sibling `actions`/`targets` next to a rule that only checks
// `flags`).
func Evaluate(rules []config.Rule, input cue.Value) ([]Match, error) {
	matches := make([]Match, 0, len(rules))
	for i, rule := range rules {
		if err := checkWhen(rule, i); err != nil {
			return nil, err
		}

		if rule.When.Subsume(input, cue.Final(), cue.Schema()) != nil {
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
