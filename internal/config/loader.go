package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	fascue "github.com/srnnkls/fas/cue"
	"github.com/srnnkls/fas/internal/diag"
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

// RulesModuleRoot is the synthetic module-root directory used by the loader.
// It must be a distinct, absolute path so CUE's overlay resolver does not
// confuse it with the real filesystem root. The path itself never touches
// disk; it only exists inside the in-memory overlay. Exported because CLI
// tooling (cmd/fas --explain) must prime its source cache with the same
// virtual prefix the overlay assigns to rule files.
//
// OS-aware so cue/load's overlay (which requires filepath.IsAbs to return
// true on the host OS) accepts every joined key. POSIX uses
// "/__fas_rules__"; Windows uses "<drive>:\__fas_rules__" because Windows
// requires a volume prefix for filepath.IsAbs to return true.
var RulesModuleRoot = computeRulesModuleRoot()

func computeRulesModuleRoot() string {
	if runtime.GOOS != "windows" {
		return "/__fas_rules__"
	}
	vol := filepath.VolumeName(os.TempDir())
	if vol == "" {
		vol = "C:"
	}
	return vol + `\__fas_rules__`
}

// rulesModulePath is the synthetic module name assigned to the rules
// directory. A module prefix is required so the overlay can host both the
// rule file and the embedded stdlib via `cue.mod/pkg/...`.
const rulesModulePath = "fas.local/rules@v0"

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
// `cue.mod/pkg/` tree hosts the embedded fas stdlib, so rule authors may
// write `import "github.com/srnnkls/fas/cue/hook"` (etc.) and reference
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

	origins := make([]fileOrigin, 0, len(names))
	for _, name := range names {
		rulePath := filepath.Join(dir, name)
		src, err := os.ReadFile(rulePath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rulePath, err)
		}

		// Structural lint runs before compilation so its taxonomy
		// (cross-rule / self-ref / unbound) shadows CUE's generic "reference
		// not found" diagnostic on the same offense.
		if err := lintRuleFile(rulePath, src); err != nil {
			return nil, err
		}

		origin, err := parseFileOrigin(rulePath, name, src)
		if err != nil {
			return nil, err
		}
		origins = append(origins, origin)
	}

	if err := checkPackageClauses(origins); err != nil {
		return nil, err
	}

	if err := checkDuplicateRuleNames(origins); err != nil {
		return nil, err
	}

	merged, err := compileRulePackage(bundle.ctx, origins, stdlibOverlay)
	if err != nil {
		return nil, err
	}

	return extractPackageRules(bundle.ruleDef, merged, origins)
}

// fileOrigin records, for one rule file, the data needed to load the directory
// as a single merged package while still attributing each rule to its source
// file: the on-disk path, the declared package name ("" if absent), the
// build-tag-safe virtual overlay name, and the top-level rule field names in
// declaration order.
type fileOrigin struct {
	path        string
	virtualName string
	packageName string
	src         []byte
	ruleFields  []string
}

// parseFileOrigin parses a rule file's AST to capture its package clause and
// its declaration-ordered top-level rule field names. Ident labels are filtered
// to regular fields (hidden `_x` helpers and `#X` definitions are skipped);
// quoted string labels are always regular fields and are included as-is.
func parseFileOrigin(rulePath, name string, src []byte) (fileOrigin, error) {
	file, err := parser.ParseFile(rulePath, src)
	if err != nil {
		return fileOrigin{}, wrapRuleLoadError(rulePath, err)
	}
	var fields []string
	for _, decl := range file.Decls {
		field, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		label, _, err := ast.LabelName(field.Label)
		if err != nil {
			continue
		}
		switch lbl := field.Label.(type) {
		case *ast.Ident:
			if isExportedOrRegular(label) {
				fields = append(fields, label)
			}
		case *ast.BasicLit:
			if lbl.Kind == token.STRING {
				fields = append(fields, label)
			}
		}
	}
	return fileOrigin{
		path:        rulePath,
		virtualName: sanitizeVirtualRuleName(name),
		packageName: file.PackageName(),
		src:         src,
		ruleFields:  fields,
	}, nil
}

// checkPackageClauses enforces AD-7: every file in a rules directory must
// declare the same single explicit `package` clause. The offending files (those
// not matching the canonical clause, or all files when no single clause exists)
// are named in an E0505 diagnostic.
func checkPackageClauses(origins []fileOrigin) error {
	explicit := map[string]struct{}{}
	for _, o := range origins {
		if o.packageName != "" {
			explicit[o.packageName] = struct{}{}
		}
	}

	var offending []string
	if len(explicit) == 1 {
		canonical := slices.Collect(maps.Keys(explicit))[0]
		for _, o := range origins {
			if o.packageName != canonical {
				offending = append(offending, filepath.Base(o.path))
			}
		}
	} else {
		for _, o := range origins {
			offending = append(offending, filepath.Base(o.path))
		}
	}

	if len(offending) == 0 {
		return nil
	}
	return packageClauseError(offending)
}

// packageClauseError builds the E0505 *diag.DiagError naming the files that
// violate the single-shared-package-clause policy.
func packageClauseError(offending []string) error {
	d := diag.Diagnostic{
		Code:     diag.E0505.Code,
		Severity: diag.SeverityError,
		Title: "rule files must share one explicit `package` clause; offending: " +
			strings.Join(offending, ", "),
		Help: diag.E0505.Help,
	}
	return diag.NewDiagError(d, nil, nil)
}

// CRP-005 will give this guard the E0504 code and a proper diagnostic.
func checkDuplicateRuleNames(origins []fileOrigin) error {
	owner := map[string]string{}
	for _, o := range origins {
		for _, name := range o.ruleFields {
			if prev, seen := owner[name]; seen && prev != o.path {
				return fmt.Errorf("rule %q is declared in both %s and %s",
					name, filepath.Base(prev), filepath.Base(o.path))
			}
			owner[name] = o.path
		}
	}
	return nil
}

// compileRulePackage loads every rule file in the directory as one merged CUE
// package so cross-file hidden helpers resolve. The overlay hosts all rule
// files (under their build-tag-safe virtual names) plus the embedded stdlib and
// the synthetic module file; the package is loaded once via the module root.
func compileRulePackage(ctx *cue.Context, origins []fileOrigin, stdlib map[string]load.Source) (cue.Value, error) {
	overlay := make(map[string]load.Source, len(stdlib)+len(origins)+1)
	maps.Copy(overlay, stdlib)
	overlay[filepath.Join(RulesModuleRoot, "cue.mod", "module.cue")] = load.FromString(
		fmt.Sprintf("module: %q\nlanguage: version: \"v0.11.0\"\n", rulesModulePath),
	)
	// CRP-013 will replace this interim guard with collision-proof disambiguation.
	keyOrigin := make(map[string]string, len(origins))
	for _, o := range origins {
		key := filepath.Join(RulesModuleRoot, o.virtualName)
		if prev, seen := keyOrigin[key]; seen && prev != o.path {
			return cue.Value{}, fmt.Errorf("rule files %s and %s map to the same overlay name %q",
				filepath.Base(prev), filepath.Base(o.path), o.virtualName)
		}
		keyOrigin[key] = o.path
		overlay[key] = load.FromBytes(o.src)
	}

	cfg := &load.Config{
		Dir:        RulesModuleRoot,
		ModuleRoot: RulesModuleRoot,
		Overlay:    overlay,
	}
	insts := load.Instances([]string{"."}, cfg)
	if len(insts) == 0 {
		return cue.Value{}, errors.New("compile rules: load returned no instances")
	}
	inst := insts[0]
	if err := inst.Err; err != nil {
		return cue.Value{}, wrapRuleLoadError(rulePackageErrPath(origins, err), err)
	}

	val := ctx.BuildInstance(inst)
	if err := val.Err(); err != nil {
		return cue.Value{}, wrapRuleLoadError(rulePackageErrPath(origins, err), err)
	}
	return val, nil
}

// rulePackageErrPath maps a merged-package compile error back to the offending
// rule file's on-disk path by matching the virtual filename in the error's
// position, falling back to the first file when no match is found.
func rulePackageErrPath(origins []fileOrigin, err error) string {
	msg := err.Error()
	for _, o := range origins {
		if strings.Contains(msg, o.virtualName) {
			return o.path
		}
	}
	return origins[0].path
}

// extractPackageRules walks each file's rule fields in origin order (files
// alphabetical, then declaration order within a file), looks each up in the
// merged value, validates and decodes it, and stamps Source with the rule's own
// originating file. Per-rule failures are accumulated so authors see every
// broken rule in a single pass.
func extractPackageRules(ruleDef, merged cue.Value, origins []fileOrigin) ([]Rule, error) {
	var out []Rule
	var loadErrs []error
	for _, o := range origins {
		for _, fieldName := range o.ruleFields {
			fieldVal := merged.LookupPath(cue.MakePath(cue.Str(fieldName)))
			if !fieldVal.Exists() {
				loadErrs = append(loadErrs, wrapFieldLoadError(o.path, fieldName, fieldVal.Err()))
				continue
			}

			unified := ruleDef.Unify(fieldVal)
			// Structural validation rejects unknown gates (closed-set #Action)
			// and shape mismatches without forcing concreteness on `when` —
			// regex/disjunction constraints there are legitimate and get
			// resolved by the evaluator at runtime.
			if err := unified.Validate(); err != nil {
				loadErrs = append(loadErrs, wrapFieldLoadError(o.path, fieldName, err))
				continue
			}
			// Unresolved references inside `when` surface on the offending leaf
			// field, not on `when` itself, so a top-level when.Err() misses a
			// typo'd stdlib reference; walk the subtree so it fails the load.
			if when := unified.LookupPath(cue.ParsePath("when")); when.Exists() {
				if err := whenFieldErr(when, 0); err != nil {
					loadErrs = append(loadErrs, wrapFieldLoadError(o.path, fieldName, err))
					continue
				}
			}
			if then := unified.LookupPath(cue.ParsePath("then")); then.Exists() {
				if err := then.Validate(cue.Concrete(true)); err != nil {
					loadErrs = append(loadErrs, wrapFieldLoadError(o.path, fieldName, err))
					continue
				}
			}

			rule, err := decodeRule(unified, fieldVal)
			if err != nil {
				loadErrs = append(loadErrs, fmt.Errorf("%s: field %q: %w", o.path, fieldName, err))
				continue
			}
			rule.Source = o.path + ":" + fieldName
			out = append(out, rule)
		}
	}
	if len(loadErrs) > 0 {
		return nil, errors.Join(loadErrs...)
	}
	return out, nil
}

// whenFieldErr walks a compiled `when` value and returns the first field that
// carries a localized error. CUE keeps reference errors (e.g. "undefined field"
// from a typo'd stdlib member such as agent.#Explor) on the offending leaf
// rather than bubbling them to the parent, so a top-level when.Err() misses
// them. Abstract pattern constraints — regex (=~), bounds (>0), disjunctions,
// list.MatchN — carry no error and are never flagged (verified against the full
// rule corpus). The depth guard is a cheap backstop against pathological nesting.
func whenFieldErr(v cue.Value, depth int) error {
	if err := v.Err(); err != nil {
		return err
	}
	if depth > 64 {
		return nil
	}
	iter, err := v.Fields(cue.All())
	if err != nil {
		// A leaf (non-struct) value has no sub-fields to descend into; Fields
		// erroring here means "not a struct", not a rule failure.
		return nil //nolint:nilerr // intentional: non-struct leaf, nothing to walk
	}
	for iter.Next() {
		if err := whenFieldErr(iter.Value(), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// wrapRuleLoadError converts a CUE diagnostic at file scope into a structured
// *diag.DiagError. The rule file path is prepended to the Diagnostic's Title
// so rendered output carries the path even when FromCueError could not resolve
// a primary source position (e.g. errors promoted without location metadata).
//
// The original CUE error stays reachable via Unwrap / errors.As so callers
// that predate the diagnostic migration — the multi-rule loader tests, the
// stdlib-import test — keep their `errors.As(err, &cueErr)` paths working.
func wrapRuleLoadError(rulePath string, err error) error {
	d := diag.FromCueError(err)
	d.Title = fmt.Sprintf("%s: %s", rulePath, d.Title)
	return diag.NewDiagError(d, nil, err)
}

// wrapFieldLoadError converts a CUE diagnostic for a specific top-level field
// into a structured *diag.DiagError. The file path and the offending field
// name both appear in the rendered Title so existing substring assertions
// ("halt" / "#Action" / "helpers" / "nonexistentDef") continue to match.
func wrapFieldLoadError(rulePath, field string, err error) error {
	d := diag.FromCueError(err)
	d.Title = fmt.Sprintf("%s: field %q does not match #Rule: %s", rulePath, field, d.Title)
	return diag.NewDiagError(d, nil, err)
}

// buildStdlibOverlay materializes the embedded fas stdlib inside the
// synthetic module's `cue.mod/pkg/github.com/srnnkls/fas/cue/` tree so
// every sub-package import (`.../cue/catalog`, `.../cue/hook`, `.../cue/tool`,
// `.../cue/agent`, `.../cue/bash`, `.../cue/path`, `.../cue/escalation`,
// `.../cue/action`, `.../cue/flag`) resolves from any rule file.
//
// Sub-directory structure is preserved: `hook/events.cue` lands at
// `pkg/.../cue/hook/events.cue` so CUE's loader treats the directory as its
// own package per the file's `package <name>` header. The core `schema.cue`
// lives at the root of the tree under `pkg/.../cue/schema.cue` for the same
// reason. The returned map is keyed by absolute-looking overlay paths
// suitable for passing straight to load.Config.Overlay.
func buildStdlibOverlay() (map[string]load.Source, error) {
	pkgRoot := filepath.Join(
		RulesModuleRoot, "cue.mod", "pkg",
		filepath.FromSlash(stdlibOverlayImportPath),
	)
	overlay := map[string]load.Source{}
	stdlib := fascue.StdlibFS()
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
		return nil, errors.New("embedded fas stdlib is empty")
	}
	return overlay, nil
}

// stdlibOverlayImportPath is the module-qualified import-path prefix each
// sub-package is reachable under. Rule authors write
// `import "github.com/srnnkls/fas/cue/hook"` (and so on); the overlay maps
// every embedded file to a path under this prefix so CUE's loader resolves
// the sub-directories as sibling packages.
const stdlibOverlayImportPath = "github.com/srnnkls/fas/cue"

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
