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
	"cuelang.org/go/cue/ast"
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
	Source string
	When   cue.Value
	// WhenSyntax is the parsed CUE AST node for the when block, retained for
	// diagnostic localization. Nil if the rule was not loaded from source.
	WhenSyntax ast.Expr       `json:"-"`
	WhenMap    map[string]any `json:",omitempty"`
	Then       *Action
	Meta       *Meta
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

// LoadRules reads `*.cue` files from dir, iterates every top-level non-hidden
// field in each file, unifies each against the `#Rule` schema, and returns the
// decoded rules. Files are visited in alphabetical order; inside a file, rules
// are emitted in declaration order. An empty directory returns an empty slice
// and a nil error. Non-.cue files are ignored.
//
// Hidden fields (`_foo`) and definitions (`#Foo`) are skipped — hidden fields
// remain addressable as local helpers from sibling rules. A non-hidden
// top-level field that does not unify with `#Rule` is a load-time error
// naming both the file and the offending field.
//
// Each rule file is compiled inside a synthetic CUE module whose
// `cue.mod/pkg/` tree hosts the embedded quae stdlib, so rule authors may
// write `import "github.com/srnnkls/quae/cue/hook"` (etc.) and reference
// `hook.#PreToolUse`, `path.#hasSystemTarget`, and friends. Rule files that
// do not import the stdlib are unaffected.
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

		// Structural lint runs before compileRuleFile so its taxonomy
		// (cross-rule / self-ref / unbound) shadows CUE's generic "reference
		// not found" diagnostic on the same offense.
		if err := lintRuleFile(rulePath, src); err != nil {
			return nil, err
		}

		fileVal, err := compileRuleFile(bundle.ctx, rulePath, src, stdlibOverlay)
		if err != nil {
			return nil, err
		}

		fileRules, err := extractFileRules(bundle.ruleDef, fileVal, rulePath)
		if err != nil {
			return nil, err
		}
		rules = append(rules, fileRules...)
	}
	return rules, nil
}

// extractFileRules walks every top-level regular field of fileVal (skipping
// hidden fields and definitions), unifies each with #Rule, and returns the
// decoded rules in declaration order.
func extractFileRules(ruleDef cue.Value, fileVal cue.Value, rulePath string) ([]Rule, error) {
	iter, err := fileVal.Fields(cue.Optional(false), cue.Definitions(false), cue.Hidden(false))
	if err != nil {
		return nil, wrapRuleLoadError(rulePath, err)
	}

	var out []Rule
	for iter.Next() {
		sel := iter.Selector()
		fieldName := sel.String()
		if sel.LabelType() == cue.StringLabel {
			fieldName = sel.Unquoted()
		}
		fieldVal := iter.Value()

		unified := ruleDef.Unify(fieldVal)
		// Structural validation rejects unknown gates (closed-set #Action)
		// and shape mismatches without forcing concreteness on `when` —
		// regex/disjunction constraints there are legitimate and get
		// resolved by the evaluator at runtime.
		if err := unified.Validate(); err != nil {
			return nil, wrapFieldLoadError(rulePath, fieldName, err)
		}
		// Unresolved references inside `when` surface as a value error on
		// the field itself. An undefined identifier from the stdlib is not
		// a legitimate non-concrete constraint; it must fail the load
		// instead of being smuggled in as silent bottom.
		if when := unified.LookupPath(cue.ParsePath("when")); when.Exists() {
			if err := when.Err(); err != nil {
				return nil, wrapFieldLoadError(rulePath, fieldName, err)
			}
		}
		if then := unified.LookupPath(cue.ParsePath("then")); then.Exists() {
			if err := then.Validate(cue.Concrete(true)); err != nil {
				return nil, wrapFieldLoadError(rulePath, fieldName, err)
			}
		}

		rule, err := decodeRule(unified, fieldVal)
		if err != nil {
			return nil, fmt.Errorf("%s: field %q: %w", rulePath, fieldName, err)
		}
		rule.Source = rulePath + ":" + fieldName
		out = append(out, rule)
	}
	return out, nil
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

// wrapFieldLoadError wraps a CUE diagnostic for a specific top-level field so
// the error names both the file and the offending field. The underlying
// cue/errors.Error is preserved for callers that use errors.As.
func wrapFieldLoadError(rulePath, field string, err error) error {
	msg := cueerrors.Details(err, nil)
	msg = strings.TrimRight(msg, "\n")
	return &ruleLoadError{path: rulePath, field: field, msg: msg, cause: err}
}

// ruleLoadError decorates a CUE diagnostic with the rule file path (and,
// when applicable, the offending top-level field name). It unwraps to the
// underlying cue/errors.Error so callers can type-assert for position
// metadata.
type ruleLoadError struct {
	path  string
	field string
	msg   string
	cause error
}

func (e *ruleLoadError) Error() string {
	switch {
	case e.field != "" && e.msg != "":
		return fmt.Sprintf("%s: field %q does not match #Rule: %s", e.path, e.field, e.msg)
	case e.field != "":
		return fmt.Sprintf("%s: field %q does not match #Rule: %v", e.path, e.field, e.cause)
	case e.msg != "":
		return fmt.Sprintf("compile %s: %s", e.path, e.msg)
	default:
		return fmt.Sprintf("compile %s: %v", e.path, e.cause)
	}
}

func (e *ruleLoadError) Unwrap() error { return e.cause }

// buildStdlibOverlay materializes the embedded quae stdlib inside the
// synthetic module's `cue.mod/pkg/github.com/srnnkls/quae/cue/` tree so
// every sub-package import (`.../cue/hook`, `.../cue/tool`, `.../cue/path`,
// `.../cue/escalation`, `.../cue/action`, `.../cue/flag`) resolves from any
// rule file.
//
// Sub-directory structure is preserved: `hook/events.cue` lands at
// `pkg/.../cue/hook/events.cue` so CUE's loader treats the directory as its
// own package per the file's `package <name>` header. The core `schema.cue`
// lives at the root of the tree under `pkg/.../cue/schema.cue` for the same
// reason. The returned map is keyed by absolute-looking overlay paths
// suitable for passing straight to load.Config.Overlay.
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
		// Preserve the directory layout so each sub-package lands at its
		// own overlay path (`hook/events.cue`, `flag/rm.cue`, ...). CUE's
		// loader resolves each directory to the package declared in its
		// files via the `package <name>` header.
		overlay[filepath.Join(pkgRoot, filepath.FromSlash(p))] = load.FromBytes(data)
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

// stdlibOverlayImportPath is the module-qualified import-path prefix each
// sub-package is reachable under. Rule authors write
// `import "github.com/srnnkls/quae/cue/hook"` (and so on); the overlay maps
// every embedded file to a path under this prefix so CUE's loader resolves
// the sub-directories as sibling packages.
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

// whenSyntax returns the parsed AST expression for a `when` value with source
// positions preserved. cue.Value.Source returns the original *ast.Field for
// the `when:` declaration; the field's Value is the struct literal we expose.
// cue.Value.Syntax is unusable here because it re-synthesizes nodes without
// positions.
func whenSyntax(when cue.Value) (ast.Expr, bool) {
	switch n := when.Source().(type) {
	case *ast.Field:
		return n.Value, true
	case ast.Expr:
		return n, true
	}
	return nil, false
}

// decodeRule extracts a Rule from a cue.Value already unified with #Rule.
// fieldVal is the original (pre-unification) rule value so the `when` AST
// retains its source positions — unification drops positional metadata.
func decodeRule(v cue.Value, fieldVal cue.Value) (Rule, error) {
	var out Rule

	if when := v.LookupPath(cue.ParsePath("when")); when.Exists() {
		out.When = when
		// Retain the parsed `when` AST with source positions for diagnostic
		// localization. The lookup goes on fieldVal, not the unified value,
		// because Unify produces a fresh computed value whose Source() is nil.
		if original := fieldVal.LookupPath(cue.ParsePath("when")); original.Exists() {
			if expr, ok := whenSyntax(original); ok {
				out.WhenSyntax = expr
			}
		}
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
