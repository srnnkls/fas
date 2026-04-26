// Package pipeline orchestrates two-phase rule evaluation.
//
// Phase 1 evaluates the global ruleset; phase 2 evaluates the project
// ruleset. Only a Blocking gate (an action of kind [config.ActionDeny])
// in phase 1 short-circuits the pipeline — non-blocking gates and all
// effects (ask, allow, inject, modify) accumulate across both phases.
// When phase 2 runs, its matches are appended to phase 1's in source
// order so downstream synthesis sees a single globally-ordered slice.
package pipeline

import (
	"fmt"
	"slices"

	"cuelang.org/go/cue"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
	"github.com/srnnkls/fas/internal/evaluator"
)

// EvaluatePhases runs two-phase evaluation:
//
//  1. Evaluate globalRules. On error, return it immediately (no phase 2).
//  2. If any phase-1 match carries a blocking action
//     (Action != nil && Action.Kind == [config.ActionDeny]), return
//     phase-1 matches (and phase-1 diagnostics) as-is and skip phase 2.
//  3. Otherwise evaluate projectRules and return the concatenation of
//     phase-1 and phase-2 matches plus diagnostics, preserving each
//     phase's source order.
//
// Diagnostics are pass-through: they only populate when the evaluator's
// package-level explain toggle is on. Only blocking gates short-circuit;
// effects never suppress phase 2.
func EvaluatePhases(globalRules, projectRules []config.Rule, input cue.Value) ([]evaluator.Match, []diag.Diagnostic, error) {
	phase1, diags1, err := evaluator.Evaluate(globalRules, input)
	if err != nil {
		return nil, nil, fmt.Errorf("pipeline phase 1: %w", err)
	}

	if slices.ContainsFunc(phase1, isBlocking) {
		return phase1, diags1, nil
	}

	phase2, diags2, err := evaluator.Evaluate(projectRules, input)
	if err != nil {
		return nil, nil, fmt.Errorf("pipeline phase 2: %w", err)
	}

	return slices.Concat(phase1, phase2), slices.Concat(diags1, diags2), nil
}

// isBlocking reports whether m carries a deny gate.
func isBlocking(m evaluator.Match) bool {
	return m.Action != nil && m.Action.Kind == config.ActionDeny
}
