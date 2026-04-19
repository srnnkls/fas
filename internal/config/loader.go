package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"cuelang.org/go/cue"
)

// ActionKind identifies which gate or effect an action represents.
type ActionKind string

const (
	ActionDeny   ActionKind = "deny"
	ActionAsk    ActionKind = "ask"
	ActionAllow  ActionKind = "allow"
	ActionModify ActionKind = "modify"
	ActionInject ActionKind = "inject"
)

// Action carries the decoded "then" clause of a rule. Fields are populated
// based on Kind; unrelated fields keep their zero values.
type Action struct {
	Kind         ActionKind
	RuleID       string
	Reason       string
	Severity     string
	Question     string
	Text         string
	Channel      string
	Tags         []string
	Priority     int
	Mode         string
	UpdatedInput map[string]any
	Allow        bool
}

// Meta mirrors the `#Meta` CUE definition.
type Meta struct {
	Requires []string
}

// Rule is a decoded CUE rule.
//
// When holds the compiled `when` clause as a cue.Value so the evaluator can
// unify it with an adapter input directly — CUE constraints such as
// `=~"^(/etc|/sys)"` are non-concrete and cannot survive a Decode into Go
// primitives. WhenMap is a best-effort Go map rendered for debugging and
// logging only; it is nil when the when clause contains non-concrete
// constraints.
type Rule struct {
	Source  string
	When    cue.Value
	WhenMap map[string]any `json:",omitempty"`
	Then    *Action
	Meta    *Meta
}

// LoadRules reads `*.cue` files from dir, unifies each against the `#Rule`
// schema, and returns decoded rules sorted deterministically by filename.
// An empty directory returns an empty slice and a nil error. Non-.cue files
// are ignored.
func LoadRules(dir string) ([]Rule, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read rules dir %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".cue") {
			continue
		}
		names = append(names, e.Name())
	}
	slices.Sort(names)

	if len(names) == 0 {
		return []Rule{}, nil
	}

	bundle, err := loadSchema()
	if err != nil {
		return nil, err
	}

	rules := make([]Rule, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		file := bundle.ctx.CompileBytes(src, cue.Filename(path))
		if err := file.Err(); err != nil {
			return nil, fmt.Errorf("compile %s: %w", path, err)
		}

		ruleVal := file.LookupPath(cue.ParsePath("rule"))
		if err := ruleVal.Err(); err != nil {
			return nil, fmt.Errorf("%s: missing top-level `rule`: %w", path, err)
		}

		unified := bundle.ruleDef.Unify(ruleVal)
		// Structural validation rejects unknown gates (closed-set #Action)
		// and shape mismatches without forcing concreteness on `when` —
		// regex/disjunction constraints there are legitimate and get
		// resolved by the evaluator at runtime.
		if err := unified.Validate(); err != nil {
			return nil, fmt.Errorf("%s: does not satisfy #Rule: %w", path, err)
		}
		// The action body, however, must be concrete so decodeAction can
		// read every field without surprises.
		if then := unified.LookupPath(cue.ParsePath("then")); then.Exists() {
			if err := then.Validate(cue.Concrete(true)); err != nil {
				return nil, fmt.Errorf("%s: action must be concrete: %w", path, err)
			}
		}

		rule, err := decodeRule(unified)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		rule.Source = path
		rules = append(rules, rule)
	}
	return rules, nil
}

// decodeRule extracts a Rule from a cue.Value already unified with #Rule.
func decodeRule(v cue.Value) (Rule, error) {
	var out Rule

	if when := v.LookupPath(cue.ParsePath("when")); when.Exists() {
		out.When = when
		// Best-effort debug map. Non-concrete constraints (e.g. regex
		// matchers) legitimately fail Decode — swallow those errors and
		// leave WhenMap nil.
		if m, err := decodeMap(when); err == nil {
			out.WhenMap = m
		}
	}

	if meta := v.LookupPath(cue.ParsePath("meta")); meta.Exists() {
		parsed, err := decodeMeta(meta)
		if err != nil {
			return Rule{}, fmt.Errorf("decode meta: %w", err)
		}
		out.Meta = parsed
	}

	if then := v.LookupPath(cue.ParsePath("then")); then.Exists() {
		action, err := decodeAction(then)
		if err != nil {
			return Rule{}, fmt.Errorf("decode then: %w", err)
		}
		out.Then = action
	}

	return out, nil
}

func decodeMeta(v cue.Value) (*Meta, error) {
	var m Meta
	if req := v.LookupPath(cue.ParsePath("requires")); req.Exists() {
		iter, err := req.List()
		if err != nil {
			return nil, fmt.Errorf("requires not a list: %w", err)
		}
		for iter.Next() {
			name, err := iter.Value().String()
			if err != nil {
				return nil, fmt.Errorf("requires element: %w", err)
			}
			m.Requires = append(m.Requires, name)
		}
	}
	return &m, nil
}

// decodeAction reads the concrete action sub-field (deny / ask / ...) from a
// #Action value and flattens it into the tagged-union Action struct.
func decodeAction(v cue.Value) (*Action, error) {
	for _, kind := range []ActionKind{ActionDeny, ActionAsk, ActionModify, ActionInject, ActionAllow} {
		sub := v.LookupPath(cue.ParsePath(string(kind)))
		if !sub.Exists() {
			continue
		}
		return decodeActionBody(kind, sub)
	}
	return nil, errors.New("no action member present")
}

func decodeActionBody(kind ActionKind, body cue.Value) (*Action, error) {
	a := &Action{Kind: kind}

	switch kind {
	case ActionAllow:
		allow, err := body.Bool()
		if err != nil {
			return nil, fmt.Errorf("allow: %w", err)
		}
		a.Allow = allow
		return a, nil

	case ActionDeny:
		if err := readString(body, "rule_id", &a.RuleID); err != nil {
			return nil, err
		}
		if err := readString(body, "reason", &a.Reason); err != nil {
			return nil, err
		}
		if err := readString(body, "severity", &a.Severity); err != nil {
			return nil, err
		}
		return a, nil

	case ActionAsk:
		if err := readString(body, "rule_id", &a.RuleID); err != nil {
			return nil, err
		}
		if err := readString(body, "reason", &a.Reason); err != nil {
			return nil, err
		}
		if err := readString(body, "question", &a.Question); err != nil {
			return nil, err
		}
		return a, nil

	case ActionInject:
		if err := readString(body, "rule_id", &a.RuleID); err != nil {
			return nil, err
		}
		if err := readString(body, "text", &a.Text); err != nil {
			return nil, err
		}
		if err := readString(body, "channel", &a.Channel); err != nil {
			return nil, err
		}
		if err := readInt(body, "priority", &a.Priority); err != nil {
			return nil, err
		}
		if tags := body.LookupPath(cue.ParsePath("tags")); tags.Exists() {
			iter, err := tags.List()
			if err != nil {
				return nil, fmt.Errorf("tags: %w", err)
			}
			for iter.Next() {
				s, err := iter.Value().String()
				if err != nil {
					return nil, fmt.Errorf("tag: %w", err)
				}
				a.Tags = append(a.Tags, s)
			}
		}
		return a, nil

	case ActionModify:
		if err := readString(body, "rule_id", &a.RuleID); err != nil {
			return nil, err
		}
		if err := readString(body, "reason", &a.Reason); err != nil {
			return nil, err
		}
		if err := readString(body, "mode", &a.Mode); err != nil {
			return nil, err
		}
		if err := readInt(body, "priority", &a.Priority); err != nil {
			return nil, err
		}
		if ui := body.LookupPath(cue.ParsePath("updated_input")); ui.Exists() {
			m, err := decodeMap(ui)
			if err != nil {
				return nil, fmt.Errorf("updated_input: %w", err)
			}
			a.UpdatedInput = m
		}
		return a, nil
	}
	return nil, fmt.Errorf("unsupported action kind %q", kind)
}

func readString(v cue.Value, field string, dst *string) error {
	sub := v.LookupPath(cue.ParsePath(field))
	if !sub.Exists() {
		return nil
	}
	s, err := sub.String()
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	*dst = s
	return nil
}

func readInt(v cue.Value, field string, dst *int) error {
	sub := v.LookupPath(cue.ParsePath(field))
	if !sub.Exists() {
		return nil
	}
	n, err := sub.Int64()
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	*dst = int(n)
	return nil
}

// decodeMap converts a CUE struct value into a generic map[string]any.
func decodeMap(v cue.Value) (map[string]any, error) {
	var raw any
	if err := v.Decode(&raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected struct, got %T", raw)
	}
	return m, nil
}
