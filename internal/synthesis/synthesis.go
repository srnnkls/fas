// Package synthesis reduces evaluator matches into a single OutputEnvelope.
//
// # Gate priority
//
// Exactly one gate action wins per evaluation. Kinds are ordered deny > ask >
// allow. Within a kind, deny tiebreaks by severity (CRITICAL > HIGH > MEDIUM >
// LOW) then rule_id ASC; ask tiebreaks by rule_id ASC only.
//
// # Inject aggregation
//
// Inject effects are additive: every matching inject contributes, sorted by
// (priority DESC, rule_id ASC). When sizeBudget is positive, the concatenated
// text is truncated so len never exceeds sizeBudget; a value of zero or less
// means unbounded. The channel field routes the text: "agent" concatenates into
// AdditionalContext, "user" concatenates into UserReason. Segments are joined
// by newline.
//
// # Modify selection
//
// Modify effects pick a single winner by priority DESC then rule_id ASC. The
// winner's UpdatedInput is JSON-marshalled into the envelope's UpdatedInput.
// When the final Category is Blocking, UpdatedInput is dropped (left nil)
// since a blocked call has no input to rewrite.
package synthesis

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/envelope"
	"github.com/srnnkls/fas/internal/evaluator"
)

// Synthesize reduces matches into a single OutputEnvelope. See the package
// doc for priority, tie-breaking, and channel-routing semantics.
func Synthesize(matches []evaluator.Match, sizeBudget int) envelope.OutputEnvelope {
	var (
		denies   []*config.Action
		asks     []*config.Action
		allows   []*config.Action
		injects  []*config.Action
		modifies []*config.Action
	)

	for _, m := range matches {
		if m.Action == nil {
			continue
		}
		switch m.Action.Kind {
		case config.ActionDeny:
			denies = append(denies, m.Action)
		case config.ActionAsk:
			asks = append(asks, m.Action)
		case config.ActionAllow:
			allows = append(allows, m.Action)
		case config.ActionInject:
			injects = append(injects, m.Action)
		case config.ActionModify:
			modifies = append(modifies, m.Action)
		}
	}

	gate := pickGate(denies, asks, allows)
	modifyWinner := pickModify(modifies)
	agentText, userText := aggregateInjects(injects, sizeBudget)

	out := envelope.OutputEnvelope{
		Category:          categorize(gate, modifyWinner),
		AdditionalContext: agentText,
	}

	applyGate(&out, gate)
	appendUserText(&out, userText)

	if out.Category != envelope.Blocking && modifyWinner != nil {
		if raw, err := json.Marshal(modifyWinner.UpdatedInput); err == nil {
			out.UpdatedInput = raw
		}
	}

	return out
}

// severityRank orders deny severities. Unknown or empty severities sort last
// so a labelled deny always beats an unlabelled one.
func severityRank(s string) int {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return 0
	case "HIGH":
		return 1
	case "MEDIUM":
		return 2
	case "LOW":
		return 3
	default:
		return 4
	}
}

// pickGate returns the single winning gate action, or nil if none matched.
func pickGate(denies, asks, allows []*config.Action) *config.Action {
	if len(denies) > 0 {
		slices.SortFunc(denies, func(a, b *config.Action) int {
			if r := severityRank(a.Severity) - severityRank(b.Severity); r != 0 {
				return r
			}
			return strings.Compare(a.RuleID, b.RuleID)
		})
		return denies[0]
	}
	if len(asks) > 0 {
		slices.SortFunc(asks, func(a, b *config.Action) int {
			return strings.Compare(a.RuleID, b.RuleID)
		})
		return asks[0]
	}
	if len(allows) > 0 {
		slices.SortFunc(allows, func(a, b *config.Action) int {
			return strings.Compare(a.RuleID, b.RuleID)
		})
		return allows[0]
	}
	return nil
}

// pickModify returns the highest-priority modify, tie-broken by rule_id ASC.
func pickModify(modifies []*config.Action) *config.Action {
	if len(modifies) == 0 {
		return nil
	}
	slices.SortFunc(modifies, func(a, b *config.Action) int {
		if a.Priority != b.Priority {
			return b.Priority - a.Priority
		}
		return strings.Compare(a.RuleID, b.RuleID)
	})
	return modifies[0]
}

// aggregateInjects sorts injects deterministically, enforces sizeBudget, and
// splits the output by channel.
func aggregateInjects(injects []*config.Action, sizeBudget int) (agent, user string) {
	if len(injects) == 0 {
		return "", ""
	}

	sorted := slices.Clone(injects)
	slices.SortFunc(sorted, func(a, b *config.Action) int {
		if a.Priority != b.Priority {
			return b.Priority - a.Priority
		}
		return strings.Compare(a.RuleID, b.RuleID)
	})

	var (
		agentParts []string
		userParts  []string
		totalBytes int
	)
	for _, inj := range sorted {
		cost := len(inj.Text)
		if totalBytes > 0 {
			cost++ // newline separator between segments
		}
		if sizeBudget > 0 && totalBytes+cost > sizeBudget {
			continue
		}
		totalBytes += cost

		switch inj.Channel {
		case "user":
			userParts = append(userParts, inj.Text)
		default:
			agentParts = append(agentParts, inj.Text)
		}
	}

	return joinSegments(agentParts, userParts)
}

// joinSegments concatenates agent and user segments with newline separators.
// Kept separate so callers can read the routing rule at a glance.
func joinSegments(agentParts, userParts []string) (agent, user string) {
	return strings.Join(agentParts, "\n"), strings.Join(userParts, "\n")
}

// categorize maps the winning gate and modify mode onto a Category.
func categorize(gate, modifyWinner *config.Action) envelope.Category {
	if gate != nil {
		switch gate.Kind {
		case config.ActionDeny:
			return envelope.Blocking
		case config.ActionAsk:
			return envelope.Asking
		}
	}
	if gate == nil && modifyWinner != nil && modifyWinner.Mode == "confirm" {
		return envelope.Asking
	}
	return envelope.Allowing
}

// applyGate writes the winning gate's reason/question onto the envelope.
// Ask combines reason and question with a newline so the user sees both.
func applyGate(out *envelope.OutputEnvelope, gate *config.Action) {
	if gate == nil {
		return
	}
	switch gate.Kind {
	case config.ActionDeny:
		out.UserReason = gate.Reason
	case config.ActionAsk:
		out.UserReason = joinNonEmpty("\n", gate.Reason, gate.Question)
	}
}

// appendUserText concatenates user-channel inject text onto UserReason,
// newline-separated from any existing gate text.
func appendUserText(out *envelope.OutputEnvelope, userText string) {
	if userText == "" {
		return
	}
	out.UserReason = joinNonEmpty("\n", out.UserReason, userText)
}

// joinNonEmpty joins non-empty segments with sep.
func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
