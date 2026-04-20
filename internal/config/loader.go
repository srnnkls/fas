package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"

	quaecue "github.com/srnnkls/quae/cue"
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

// rulesModuleRoot is the synthetic module-root directory used by the loader.
// It must be a distinct, absolute-looking path so CUE's overlay resolver does
// not confuse it with the real filesystem root. The path itself never touches
// disk; it only exists inside the in-memory overlay.
const rulesModuleRoot = "/__quae_rules__"

// rulesModulePath is the synthetic module name assigned to the rules
// directory. A module prefix is required so the overlay can host both the
// rule file and the embedded stdlib via `cue.mod/pkg/...`.
const rulesModulePath = "quae.local/rules@v0"

// LoadRules reads `*.cue` files from dir, unifies each against the `#Rule`
// schema, and returns decoded rules sorted deterministically by filename.
// An empty directory returns an empty slice and a nil error. Non-.cue files
// are ignored.
//
// Each rule file is compiled inside a synthetic CUE module whose
// `cue.mod/pkg/` tree hosts the embedded quae stdlib, so rule authors may
// write `import "github.com/srnnkls/quae/cue:quae"` and reference
// `quae.#hasSystemTarget`, `quae.#HasRmForce`, etc. Rule files that do not
// import the stdlib are unaffected.
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

	stdlibOverlay, err := buildStdlibOverlay()
	if err != nil {
		return nil, err
	}

	rules := make([]Rule, 0, len(names))
	for _, name := range names {
		rulePath := filepath.Join(dir, name)
		src, err := os.ReadFile(rulePath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rulePath, err)
		}

		file, err := compileRuleFile(bundle.ctx, rulePath, src, stdlibOverlay)
		if err != nil {
			return nil, err
		}

		ruleVal := file.LookupPath(cue.ParsePath("rule"))
		if err := ruleVal.Err(); err != nil {
			return nil, fmt.Errorf("%s: missing top-level `rule`: %w", rulePath, err)
		}

		unified := bundle.ruleDef.Unify(ruleVal)
		// Structural validation rejects unknown gates (closed-set #Action)
		// and shape mismatches without forcing concreteness on `when` —
		// regex/disjunction constraints there are legitimate and get
		// resolved by the evaluator at runtime.
		if err := unified.Validate(); err != nil {
			return nil, fmt.Errorf("%s: does not satisfy #Rule: %w", rulePath, err)
		}
		// Unresolved references inside the `when` clause surface as a value
		// error on the field itself rather than a validation failure on the
		// rule as a whole — CUE tolerates non-concrete `when` bodies on
		// purpose. An undefined identifier from the stdlib is not a
		// non-concrete constraint, though; it's a hard mistake that must
		// fail the load instead of being smuggled in as silent bottom.
		if when := unified.LookupPath(cue.ParsePath("when")); when.Exists() {
			if err := when.Err(); err != nil {
				return nil, wrapRuleLoadError(rulePath, err)
			}
		}
		// The action body, however, must be concrete so decodeAction can
		// read every field without surprises.
		if then := unified.LookupPath(cue.ParsePath("then")); then.Exists() {
			if err := then.Validate(cue.Concrete(true)); err != nil {
				return nil, fmt.Errorf("%s: action must be concrete: %w", rulePath, err)
			}
		}

		rule, err := decodeRule(unified)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rulePath, err)
		}
		rule.Source = rulePath
		rules = append(rules, rule)
	}
	return rules, nil
}

// compileRuleFile evaluates a rule file with stdlib imports resolved.
//
// Rule files are compiled inside a synthetic CUE module whose overlay maps
// the user's rule file plus a `cue.mod/pkg/github.com/srnnkls/quae/cue/`
// tree populated from the embedded stdlib. When the rule file has no stdlib
// import, CUE still resolves fine — the overlay's pkg tree is simply unused.
func compileRuleFile(ctx *cue.Context, rulePath string, src []byte, stdlib map[string]load.Source) (cue.Value, error) {
	overlay := make(map[string]load.Source, len(stdlib)+2)
	maps.Copy(overlay, stdlib)
	// Synthetic module root. Every overlay path must share this prefix so
	// CUE treats the virtual directory as the module being loaded.
	overlay[filepath.Join(rulesModuleRoot, "cue.mod", "module.cue")] = load.FromString(
		fmt.Sprintf("module: %q\nlanguage: version: \"v0.11.0\"\n", rulesModulePath),
	)
	// Virtual filename stripped of any suffix CUE treats as a build-tag.
	// Files ending in `_tool.cue` or `_test.cue` are filtered out in
	// non-cmd / non-test mode, so we always load under a neutral name.
	// Diagnostic context for the author comes from wrapRuleLoadError, which
	// prepends the real rule file path.
	virtualName := sanitizeVirtualRuleName(filepath.Base(rulePath))
	overlay[filepath.Join(rulesModuleRoot, virtualName)] = load.FromBytes(src)

	cfg := &load.Config{
		Dir:        rulesModuleRoot,
		ModuleRoot: rulesModuleRoot,
		Overlay:    overlay,
	}
	insts := load.Instances([]string{virtualName}, cfg)
	if len(insts) == 0 {
		return cue.Value{}, fmt.Errorf("compile %s: load returned no instances", rulePath)
	}
	inst := insts[0]
	if err := inst.Err; err != nil {
		return cue.Value{}, wrapRuleLoadError(rulePath, err)
	}

	val := ctx.BuildInstance(inst)
	if err := val.Err(); err != nil {
		return cue.Value{}, wrapRuleLoadError(rulePath, err)
	}
	return val, nil
}

// wrapRuleLoadError wraps a CUE diagnostic with the offending rule file path
// while preserving the original cue/errors.Error so callers can retrieve
// structured position information via errors.As.
func wrapRuleLoadError(rulePath string, err error) error {
	// Flatten CUE's error chain so each diagnostic is visible in the
	// rendered message; cue/errors.Error values format as a single line
	// unless walked with Details.
	msg := cueerrors.Details(err, nil)
	msg = strings.TrimRight(msg, "\n")
	return &ruleLoadError{path: rulePath, msg: msg, cause: err}
}

// ruleLoadError decorates a CUE diagnostic with the rule file path and
// unwraps to the underlying cue/errors.Error so callers can type-assert for
// position metadata.
type ruleLoadError struct {
	path  string
	msg   string
	cause error
}

func (e *ruleLoadError) Error() string {
	if e.msg == "" {
		return fmt.Sprintf("compile %s: %v", e.path, e.cause)
	}
	return fmt.Sprintf("compile %s: %s", e.path, e.msg)
}

func (e *ruleLoadError) Unwrap() error { return e.cause }

// buildStdlibOverlay materializes the embedded quae stdlib inside the
// synthetic module's `cue.mod/pkg/github.com/srnnkls/quae/cue/` tree so
// `import "github.com/srnnkls/quae/cue:quae"` resolves from any rule file.
//
// Every `.cue` file in the embedded tree is flattened into the package root:
// nested files like `flags/rm.cue` get placed next to `quae.cue` under a
// disambiguated basename. A CUE package is defined by a single directory,
// and the shipped files all declare `package quae`, so flattening unifies
// them into one importable package while preserving their per-file bodies.
// The returned map is keyed by absolute-looking overlay paths suitable for
// passing straight to load.Config.Overlay.
func buildStdlibOverlay() (map[string]load.Source, error) {
	pkgRoot := filepath.Join(
		rulesModuleRoot, "cue.mod", "pkg",
		filepath.FromSlash(stdlibOverlayImportPath),
	)
	overlay := map[string]load.Source{}
	stdlib := quaecue.StdlibFS()
	err := fs.WalkDir(stdlib, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if path.Ext(p) != ".cue" {
			return nil
		}
		data, err := fs.ReadFile(stdlib, p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		// Replace slashes with underscores so nested files don't create
		// sub-packages; `flags/rm.cue` → `flags__rm.cue`.
		flatName := strings.ReplaceAll(p, "/", "__")
		overlay[filepath.Join(pkgRoot, flatName)] = load.FromBytes(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(overlay) == 0 {
		return nil, errors.New("embedded quae stdlib is empty")
	}
	return overlay, nil
}

// stdlibOverlayImportPath is the module-qualified import path the stdlib is
// reachable under. Must match the prefix rule authors type in their
// `import` statements (`github.com/srnnkls/quae/cue:quae`), minus the package
// qualifier — the qualifier is applied per-file by the package clause inside
// each embedded source.
const stdlibOverlayImportPath = "github.com/srnnkls/quae/cue"

// sanitizeVirtualRuleName rewrites a rule filename so it bypasses CUE's
// build-tag filename suffixes (`_tool.cue`, `_test.cue`) which would
// otherwise exclude the file from a non-cmd / non-test build. The result is
// still a `.cue` file so the loader will consider it.
func sanitizeVirtualRuleName(name string) string {
	ext := filepath.Ext(name)
	if ext != ".cue" {
		// Defensive: LoadRules already filters to .cue, but keep the
		// function robust if callers change.
		return name
	}
	stem := strings.TrimSuffix(name, ext)
	for _, suffix := range []string{"_tool", "_test"} {
		if before, ok := strings.CutSuffix(stem, suffix); ok {
			stem = before + "_rule"
			break
		}
	}
	return stem + ext
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
