// Command fas is the CLI entry point for the Fas policy engine.
//
// fas eval reads a vendor-native hook payload on stdin, evaluates it against
// two layered rule sets (global + project), and writes the vendor-native
// response on stdout. The pipeline is:
//
//	stdin JSON -> adapter.ParseInput
//	           -> parser.Preprocess (tool_input.parsed)
//	           -> cue.Value encoding
//	           -> config.LoadRules (global, then project)
//	           -> adapter capability check (reject unsupported effects)
//	           -> pipeline.EvaluatePhases
//	           -> synthesis.Synthesize
//	           -> adapter.RenderOutput -> stdout
//
// The CLI is fail-open by default: engine-level errors (malformed input,
// preprocessor failure) still emit an Allowing envelope so a buggy hook can
// never wedge a user's workflow. Pass --fail-closed to flip that behaviour
// for production enforcement. Rule-loading errors are always fatal: a
// misconfigured policy is a configuration bug, not an engine bug.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/srnnkls/fas/internal/adapter"
	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/debuglog"
	"github.com/srnnkls/fas/internal/diag"
	"github.com/srnnkls/fas/internal/envelope"
	"github.com/srnnkls/fas/internal/evaluator"
	"github.com/srnnkls/fas/internal/parser"
	"github.com/srnnkls/fas/internal/pipeline"
	"github.com/srnnkls/fas/internal/synthesis"
)

// defaultSizeBudget caps concatenated inject text so a runaway rule cannot
// blow past reasonable hook-response sizes. 8 KiB covers every realistic
// PreToolUse injection; larger payloads are almost certainly bugs.
const defaultSizeBudget = 8192

// defaultProjectRulesDir is the conventional location for per-repo rules.
const defaultProjectRulesDir = ".fas/rules"

// defaultGlobalRulesSubpath is joined onto $HOME/.config to find the
// user-global rules directory when --global-config is omitted.
const defaultGlobalRulesSubpath = ".config/fas/rules"

// version is overridden at release-build time via -ldflags
// "-X main.version=v0.1.0". Local `go build` keeps the "dev" sentinel.
var version = "dev"

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr, os.Args[1:]))
}

// run is the in-process entry point exercised by tests. It returns the
// intended process exit code instead of calling os.Exit so test harnesses can
// drive the CLI with bytes.Buffers.
func run(stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	// `--version` short-circuits before any subcommand routing or rule
	// loading: it's a metadata query, not a request to evaluate anything.
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-version") {
		_, _ = fmt.Fprintf(stdout, "fas %s\n", version)
		return 0
	}

	// The `explain` subcommand reuses stdin/rule-loading plumbing but owns
	// its own flag set, rule-id resolution, and exit-code mapping — handle
	// it before the eval path peels off its optional leading token.
	if len(args) > 0 && args[0] == "explain" {
		return runExplain(stdin, stdout, stderr, args[1:])
	}

	// The `vet` subcommand validates rule files without requiring stdin
	// input — useful for CI, pre-commit hooks, and rule authoring.
	if len(args) > 0 && args[0] == "vet" {
		return runVet(stdout, stderr, args[1:])
	}

	// Drop an optional leading "eval" subcommand so the CLI accepts both
	// `fas eval --harness claude` and `fas --harness claude`. The v0.1
	// binary only has one subcommand; insisting on it buys nothing.
	if len(args) > 0 && args[0] == "eval" {
		args = args[1:]
	}

	opts, helpRequested, err := parseFlags(args, stderr)
	if err != nil {
		// flag.Parse already wrote its own diagnostic to the provided output;
		// a redundant message here would double-print.
		return 2
	}
	if helpRequested {
		printUsage(stdout)
		return 0
	}

	rec := debuglog.Open(os.Getenv("FAS_LOG"), os.Getenv("FAS_LOG_TTL"), args, stderr)

	ad, ok := selectAdapter(opts.harness)
	if !ok {
		errorf(stderr, "unknown harness %q; supported: %s\n",
			opts.harness, strings.Join(supportedHarnesses(), ", "))
		return exitWithLog(rec, 2)
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		errorf(stderr, "read stdin: %v\n", err)
		return exitWithLog(rec, 1)
	}
	rec.SetRawInput(raw)

	globalRules, err := loadRulesDir(opts.globalConfig)
	if err != nil {
		errorln(stderr, err)
		return exitWithLog(rec, 1)
	}
	projectRules, err := loadRulesDir(opts.projectConfig)
	if err != nil {
		errorln(stderr, err)
		return exitWithLog(rec, 1)
	}
	rec.SetRules(ruleIDs(globalRules), ruleIDs(projectRules))

	if err := checkAdapterCapabilities(ad, globalRules, projectRules); err != nil {
		errorln(stderr, err)
		return exitWithLog(rec, 1)
	}

	// FAS_EXPLAIN acts as an env fallback for `--explain=missed` but only
	// when the user did not pass the flag explicitly. Read on every run so an
	// in-process test harness sees fresh env state, never latched from a
	// previous invocation.
	if !opts.explain.set && isTruthyEnv(os.Getenv("FAS_EXPLAIN")) {
		opts.explain = explainFlag{set: true, value: explainMissed}
	}

	// The evaluator's explain toggle is a process-wide atomic. Reset it on
	// every run so an in-process test harness cannot leak the previous run's
	// state into the next invocation.
	evaluator.SetExplainEnabled(opts.explain.set)

	response, matches, diags, err := evaluate(ad, raw, globalRules, projectRules, opts.failClosed)
	if err != nil {
		errorf(stderr, "render response: %v\n", err)
		return exitWithLog(rec, 1)
	}
	rec.SetMatches(matchSummaries(matches))
	rec.SetOutput(response)

	if _, err := stdout.Write(response); err != nil {
		errorf(stderr, "write stdout: %v\n", err)
		return exitWithLog(rec, 1)
	}

	if opts.explain.set {
		renderExplain(stderr, opts.explain.value, opts.format, opts.color, matches, diags, globalRules, projectRules)
	}
	return exitWithLog(rec, 0)
}

// runExplain implements `fas explain <rule_id> < input.json`. It loads both
// rule sets, resolves rule_id with project-wins tie-break, evaluates the
// single rule against stdin input, and maps (match, no-match, engine error)
// to exit codes 0/1/2. The subcommand always localizes (implicit
// explain-on), and resets the evaluator toggle on the way out so subsequent
// in-process `eval` calls without --explain do not inherit it.
func runExplain(stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	// `--code <code>` is an offline fast-path: it prints the registered
	// help for an error code and exits. Pre-scan args before the rule_id
	// guard so the flag wins over any positional token, and so rule
	// loading and stdin reads are skipped entirely.
	if code, ok, valid := extractCodeFlag(args); ok {
		if !valid {
			errorf(stderr, "unknown error code %q\n", code)
			return 2
		}
		info, found := diag.LookupCode(code)
		if !found {
			errorf(stderr, "unknown error code %q\n", code)
			return 2
		}
		_, _ = fmt.Fprintf(stdout, "%s\n\n%s\n", info.Code, info.Help)
		return 0
	}

	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		errorf(stderr, "usage: fas explain <rule_id> [--harness <name>] [--config <path>] [--global-config <path>]\n")
		return 2
	}
	ruleID := args[0]
	rest := args[1:]

	fs := flag.NewFlagSet("fas explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { writeUsage(stderr) }

	defaultGlobal, err := defaultGlobalConfigDir()
	if err != nil {
		defaultGlobal = defaultGlobalRulesSubpath
	}
	harness := "claude"
	projectConfig := defaultProjectRulesDir
	globalConfig := defaultGlobal
	fs.StringVar(&harness, "harness", harness,
		"vendor harness whose hook protocol to speak (e.g. claude)")
	fs.StringVar(&projectConfig, "config", projectConfig,
		"path to the project rules directory")
	fs.StringVar(&globalConfig, "global-config", globalConfig,
		"path to the user-global rules directory")
	var format formatFlag
	var color colorFlag
	fs.Var(&format, "format",
		"diagnostic output format: text|json|sarif (default text)")
	fs.Var(&color, "color",
		"color mode for text diagnostics: auto|always|never (default auto)")

	if err := fs.Parse(rest); err != nil {
		return 2
	}
	resolvedFormat, ferr := resolveFormat(format, os.Getenv("FAS_FORMAT"))
	if ferr != nil {
		errorln(stderr, ferr)
		return 2
	}
	resolvedColor, cerr := resolveColor(color, os.Getenv("FAS_COLOR"), os.Getenv("NO_COLOR"))
	if cerr != nil {
		errorln(stderr, cerr)
		return 2
	}

	ad, ok := selectAdapter(harness)
	if !ok {
		errorf(stderr, "unknown harness %q; supported: %s\n",
			harness, strings.Join(supportedHarnesses(), ", "))
		return 2
	}

	globalRules, err := loadRulesDir(globalConfig)
	if err != nil {
		errorln(stderr, err)
		return 2
	}
	projectRules, err := loadRulesDir(projectConfig)
	if err != nil {
		errorln(stderr, err)
		return 2
	}

	// Project-wins tie-break: project rules are searched first so a rule_id
	// present in both sets resolves to the project definition.
	resolved, ok := findRuleByID(ruleID, projectRules, globalRules)
	if !ok {
		errorf(stderr, "rule_id %q not found in project or global rules\n", ruleID)
		return 2
	}

	if err := checkAdapterCapabilities(ad, []config.Rule{resolved}); err != nil {
		errorln(stderr, err)
		return 2
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		errorf(stderr, "read stdin: %v\n", err)
		return 2
	}

	input, _, err := prepareInput(ad, raw)
	if err != nil {
		errorln(stderr, err)
		return 2
	}
	cueInput, err := encodeInput(input)
	if err != nil {
		errorln(stderr, err)
		return 2
	}

	// Implicit explain-on for this subcommand: no-match must always produce
	// a localized diagnostic on stderr. Reset on exit so a subsequent
	// in-process `eval` without --explain does not inherit the toggle.
	evaluator.SetExplainEnabled(true)
	defer evaluator.SetExplainEnabled(false)

	matches, diags, err := evaluator.Evaluate([]config.Rule{resolved}, cueInput)
	if err != nil {
		if errors.Is(err, evaluator.ErrInvalidInput) {
			errorln(stderr, "invalid input for evaluator")
			return 2
		}
		errorf(stderr, "evaluate: %v\n", err)
		return 2
	}

	if len(matches) > 0 {
		return 0
	}

	writeExplainDiagnostics(stderr, diags, resolvedFormat, resolvedColor, []config.Rule{resolved})
	return 1
}

// writeExplainDiagnostics is the `fas explain` variant of writeDiagnostics:
// same format dispatch, but without the per-diag `rule_id:` header (the
// caller has already resolved a single rule_id, so the per-diag attribution
// step is redundant noise for this subcommand).
func writeExplainDiagnostics(w io.Writer, diags []diag.Diagnostic, format outputFormat, color colorMode, rules []config.Rule) {
	if len(diags) == 0 {
		return
	}
	switch format {
	case formatJSON:
		_ = diag.RenderJSONStream(w, diags)
	case formatSARIF:
		_, _ = w.Write(diag.RenderSARIF(diags))
	default:
		palette := paletteFor(color, isTTY(os.Stderr), os.Getenv("NO_COLOR"))
		src := primeFileCache(rules)
		for i := range diags {
			_, _ = io.WriteString(w, diag.RenderWithPalette(diags[i], src, palette))
		}
	}
}

// extractCodeFlag scans args for a `--code` or `-code` flag (either the
// space-separated `--code VAL` or the equals form `--code=VAL`). It returns
// the value, whether the flag was found at all, and whether the value is a
// non-empty token. The scan is bounded to runExplain's arg slice so the
// fast-path can decide precedence without constructing a full FlagSet (which
// would force it to also declare every other explain flag).
func extractCodeFlag(args []string) (code string, found bool, valid bool) {
	for i := range args {
		a := args[i]
		switch {
		case a == "--code" || a == "-code":
			if i+1 >= len(args) {
				return "", true, false
			}
			v := args[i+1]
			return v, true, v != ""
		case strings.HasPrefix(a, "--code="):
			v := strings.TrimPrefix(a, "--code=")
			return v, true, v != ""
		case strings.HasPrefix(a, "-code="):
			v := strings.TrimPrefix(a, "-code=")
			return v, true, v != ""
		}
	}
	return "", false, false
}

// findRuleByID walks ruleSets in order and returns the first rule whose
// `then.rule_id` equals id. Callers supply project rules before global rules
// to implement the project-wins tie-break for ambiguous ids.
func findRuleByID(id string, ruleSets ...[]config.Rule) (config.Rule, bool) {
	for _, rules := range ruleSets {
		for _, r := range rules {
			if r.Then != nil && r.Then.RuleID == id {
				return r, true
			}
		}
	}
	return config.Rule{}, false
}

// errorf writes a formatted diagnostic to w, discarding the write error.
// Diagnostics to stderr have no meaningful recovery path; propagating a write
// failure would force every call site into the same no-op.
func errorf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

// errorln writes a newline-terminated diagnostic to w, discarding the write
// error for the same reason as errorf.
func errorln(w io.Writer, a ...any) {
	_, _ = fmt.Fprintln(w, a...)
}

// explainFilter selects which diagnostics --explain emits to stderr.
type explainFilter int

const (
	explainOff explainFilter = iota
	explainMissed
	explainFired
	explainBoth
)

// explainFlag is a flag.Value backing --explain. Bare `--explain` (no =value)
// defaults to `missed`; `--explain=fired|missed|both` picks a mode; invalid
// values surface a listing of accepted values.
type explainFlag struct {
	set   bool
	value explainFilter
}

// IsBoolFlag reports that --explain may appear without an =value. When the
// flag package encounters bare --explain it calls Set("true") as a sentinel;
// we translate that into the `missed` default.
func (f *explainFlag) IsBoolFlag() bool { return true }

func (f *explainFlag) String() string {
	if f == nil {
		return ""
	}
	switch f.value {
	case explainMissed:
		return "missed"
	case explainFired:
		return "fired"
	case explainBoth:
		return "both"
	default:
		return ""
	}
}

func (f *explainFlag) Set(raw string) error {
	f.set = true
	switch raw {
	// "true" is the sentinel flag.Parse uses for bool-style bare flags.
	case "true", "", "missed":
		f.value = explainMissed
		return nil
	case "fired":
		f.value = explainFired
		return nil
	case "both":
		f.value = explainBoth
		return nil
	default:
		return fmt.Errorf("invalid --explain value %q: must be one of fired, missed, both", raw)
	}
}

// isTruthyEnv reports whether an environment value means "on". Matches
// {1, true, yes} case-insensitively; everything else (including filter-mode
// words like "fired"/"missed"/"both") is treated as off. FAS_EXPLAIN is a
// truthiness switch, not a filter channel — truthy routes through the same
// code path as `--explain=missed`.
func isTruthyEnv(v string) bool {
	switch {
	case v == "1":
		return true
	case strings.EqualFold(v, "true"):
		return true
	case strings.EqualFold(v, "yes"):
		return true
	default:
		return false
	}
}

// cliOptions bundles the parsed flag state so run stays flat.
type cliOptions struct {
	harness       string
	projectConfig string
	globalConfig  string
	failClosed    bool
	explain       explainFlag
	format        outputFormat
	color         colorMode
}

// outputFormat enumerates diagnostic emission formats wired through to the
// renderer. `formatUnset` distinguishes "no flag provided" from "text" so
// env-var fallback (FAS_FORMAT) only fires when the flag is absent.
type outputFormat int

const (
	formatUnset outputFormat = iota
	formatText
	formatJSON
	formatSARIF
)

// colorMode enumerates --color values. `colorUnset` is the pre-resolution
// sentinel that triggers FAS_COLOR / NO_COLOR fallback; the other three
// map 1:1 to the user-visible flag vocabulary.
type colorMode int

const (
	colorUnset colorMode = iota
	colorAuto
	colorAlways
	colorNever
)

// parseFormatValue decodes a --format / FAS_FORMAT string. Empty and
// "text" map to text; "json" and "sarif" are recognised; everything else is
// an error surfaced to the caller for an exit-2 diagnostic.
func parseFormatValue(s string) (outputFormat, error) {
	switch s {
	case "", "text":
		return formatText, nil
	case "json":
		return formatJSON, nil
	case "sarif":
		return formatSARIF, nil
	default:
		return formatUnset, fmt.Errorf("invalid format %q: must be one of text, json, sarif", s)
	}
}

// parseColorValue decodes a --color / FAS_COLOR string. Empty treats as
// auto for env-var precedence callers.
func parseColorValue(s string) (colorMode, error) {
	switch s {
	case "", "auto":
		return colorAuto, nil
	case "always":
		return colorAlways, nil
	case "never":
		return colorNever, nil
	default:
		return colorUnset, fmt.Errorf("invalid color %q: must be one of auto, always, never", s)
	}
}

// formatFlag backs --format on both subcommands. It tracks whether the
// user set it explicitly so env-var precedence (flag > env > default) can
// fire only when the flag is absent.
type formatFlag struct {
	set   bool
	value outputFormat
}

func (f *formatFlag) String() string {
	if f == nil {
		return "text"
	}
	switch f.value {
	case formatJSON:
		return "json"
	case formatSARIF:
		return "sarif"
	default:
		return "text"
	}
}

func (f *formatFlag) Set(raw string) error {
	v, err := parseFormatValue(raw)
	if err != nil {
		return err
	}
	f.set = true
	f.value = v
	return nil
}

// colorFlag backs --color on both subcommands. Like formatFlag, the `set`
// bit disambiguates default-from-explicit so env vars and flags compose
// correctly.
type colorFlag struct {
	set   bool
	value colorMode
}

func (f *colorFlag) String() string {
	if f == nil {
		return "auto"
	}
	switch f.value {
	case colorAlways:
		return "always"
	case colorNever:
		return "never"
	default:
		return "auto"
	}
}

func (f *colorFlag) Set(raw string) error {
	v, err := parseColorValue(raw)
	if err != nil {
		return err
	}
	f.set = true
	f.value = v
	return nil
}

// resolveFormat applies precedence: flag > env > default. Unknown env
// values produce a terse wrapped error the caller surfaces on stderr.
func resolveFormat(fv formatFlag, env string) (outputFormat, error) {
	if fv.set {
		return fv.value, nil
	}
	if env != "" {
		v, err := parseFormatValue(env)
		if err != nil {
			return formatText, fmt.Errorf("FAS_FORMAT: %w", err)
		}
		return v, nil
	}
	return formatText, nil
}

// resolveColor applies precedence across --color / FAS_COLOR / NO_COLOR.
// Order: explicit flag wins; else FAS_COLOR (fas-specific) wins over
// NO_COLOR (community convention); else auto with NO_COLOR respected.
// Unknown env values return an error so startup can exit 2.
func resolveColor(cv colorFlag, fasColorEnv, noColorEnv string) (colorMode, error) {
	if cv.set {
		return cv.value, nil
	}
	if fasColorEnv != "" {
		v, err := parseColorValue(fasColorEnv)
		if err != nil {
			return colorAuto, fmt.Errorf("FAS_COLOR: %w", err)
		}
		return v, nil
	}
	if noColorEnv != "" {
		return colorNever, nil
	}
	return colorAuto, nil
}

// isTTY reports whether f is attached to a terminal (character device).
// The check uses only os.FileInfo.Mode so it works without importing
// golang.org/x/term — the go.mod does not pull it in transitively and this
// scope forbids new deps.
func isTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// paletteFor converts a resolved colorMode + TTY probe into a concrete
// diag.Palette. auto defers to the TTY probe AND the NO_COLOR community
// convention; always/never are unconditional.
func paletteFor(mode colorMode, tty bool, noColorEnv string) diag.Palette {
	switch mode {
	case colorAlways:
		return diag.ANSIPalette{}
	case colorNever:
		return diag.NoColorPalette{}
	}
	if tty && noColorEnv == "" {
		return diag.ANSIPalette{}
	}
	return diag.NoColorPalette{}
}

// parseFlags reads args into a cliOptions. The returned bool is true when the
// user asked for help; callers should print usage and exit 0.
func parseFlags(args []string, stderr io.Writer) (cliOptions, bool, error) {
	fs := flag.NewFlagSet("fas eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { writeUsage(stderr) }

	defaultGlobal, err := defaultGlobalConfigDir()
	if err != nil {
		// Fall back to a bare relative path so flag parsing still works even
		// when HOME is unset; loadRulesDir will treat it as "does not exist".
		defaultGlobal = defaultGlobalRulesSubpath
	}

	opts := cliOptions{
		harness:       "claude",
		projectConfig: defaultProjectRulesDir,
		globalConfig:  defaultGlobal,
	}
	fs.StringVar(&opts.harness, "harness", opts.harness,
		"vendor harness whose hook protocol to speak (e.g. claude)")
	fs.StringVar(&opts.projectConfig, "config", opts.projectConfig,
		"path to the project rules directory")
	fs.StringVar(&opts.globalConfig, "global-config", opts.globalConfig,
		"path to the user-global rules directory")
	fs.BoolVar(&opts.failClosed, "fail-closed", false,
		"on engine error, emit a Blocking envelope instead of Allowing")
	fs.Var(&opts.explain, "explain",
		"emit diagnostics to stderr: fired|missed|both (default missed when bare)")
	var format formatFlag
	var color colorFlag
	fs.Var(&format, "format",
		"diagnostic output format: text|json|sarif (default text)")
	fs.Var(&color, "color",
		"color mode for text diagnostics: auto|always|never (default auto)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, true, nil
		}
		return opts, false, err
	}
	opts.format = formatText
	opts.color = colorAuto
	f, rerr := resolveFormat(format, os.Getenv("FAS_FORMAT"))
	if rerr != nil {
		errorln(stderr, rerr)
		return opts, false, rerr
	}
	opts.format = f
	c, rerr := resolveColor(color, os.Getenv("FAS_COLOR"), os.Getenv("NO_COLOR"))
	if rerr != nil {
		errorln(stderr, rerr)
		return opts, false, rerr
	}
	opts.color = c
	return opts, false, nil
}

// defaultGlobalConfigDir resolves ~/.config/fas/rules using os.UserHomeDir
// so tilde expansion never leaks a literal "~" into downstream path handling.
func defaultGlobalConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultGlobalRulesSubpath), nil
}

// selectAdapter looks up a harness by name. The adapter registry lives inline
// so adding Cursor or OpenCode later is a one-line change.
func selectAdapter(name string) (adapter.Adapter, bool) {
	switch name {
	case "claude":
		return adapter.ClaudeCode{}, true
	default:
		return nil, false
	}
}

// supportedHarnesses returns the registry names in a stable order so error
// messages stay deterministic.
func supportedHarnesses() []string {
	return []string{"claude"}
}

// loadRulesDir wraps config.LoadRules with the "missing dir is empty" policy
// that the CLI guarantees to users. Rule-load failures propagate as-is so the
// underlying filename surfaces in the error message.
func loadRulesDir(dir string) ([]config.Rule, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat rules dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("rules path %s is not a directory", dir)
	}
	return config.LoadRules(dir)
}

// checkAdapterCapabilities rejects rulesets that emit effects the selected
// adapter cannot honour. For Claude Code this flags any modify action because
// PreToolUse has no updatedInput channel — the rule is a configuration bug,
// not an engine bug, so we surface it before evaluating.
func checkAdapterCapabilities(ad adapter.Adapter, ruleSets ...[]config.Rule) error {
	if ad.AllowsModify() {
		return nil
	}
	for _, rules := range ruleSets {
		for _, r := range rules {
			if r.Then == nil || r.Then.Kind != config.ActionModify {
				continue
			}
			return fmt.Errorf(
				"rule %q in %s emits modify, but %s harness does not support it",
				r.Then.RuleID, r.Source, ad.Name(),
			)
		}
	}
	return nil
}

// evaluate runs the full pipeline and returns the rendered response bytes
// plus the matches and diagnostics observed on the happy path. Engine-level
// errors (parse, preprocess) are folded into a fail-open/closed envelope
// here; only adapter render failures bubble up as real errors. Matches and
// diagnostics are nil on the error-folded paths because they are only
// meaningful when evaluation reached phase results.
func evaluate(
	ad adapter.Adapter,
	raw []byte,
	globalRules, projectRules []config.Rule,
	failClosed bool,
) ([]byte, []evaluator.Match, []diag.Diagnostic, error) {
	input, hookEventName, engineErr := prepareInput(ad, raw)
	if engineErr != nil {
		out := fallbackEnvelope(engineErr, failClosed)
		resp, err := ad.RenderOutput(out, hookEventName)
		return resp, nil, nil, err
	}

	cueInput, err := encodeInput(input)
	if err != nil {
		out := fallbackEnvelope(err, failClosed)
		resp, rerr := ad.RenderOutput(out, hookEventName)
		return resp, nil, nil, rerr
	}

	matches, diags, err := pipeline.EvaluatePhases(globalRules, projectRules, cueInput)
	if err != nil {
		out := fallbackEnvelope(err, failClosed)
		resp, rerr := ad.RenderOutput(out, hookEventName)
		return resp, nil, nil, rerr
	}

	out := synthesis.Synthesize(matches, defaultSizeBudget)
	resp, err := ad.RenderOutput(out, hookEventName)
	return resp, matches, diags, err
}

// prepareInput runs the adapter parse and preprocessor. It returns the
// enriched envelope.Input along with the hook event name the adapter should
// use when rendering. On engine error the hook event name is best-effort —
// the adapter renders a fallback envelope regardless of whether the payload
// was well-formed enough to extract one.
func prepareInput(ad adapter.Adapter, raw []byte) (*envelope.Input, string, error) {
	input, err := ad.ParseInput(json.RawMessage(raw))
	if err != nil {
		return nil, peekHookEventName(raw), fmt.Errorf("adapter parse: %w", err)
	}

	enriched, err := runPreprocessor(input)
	if err != nil {
		return nil, input.HookEventName, fmt.Errorf("preprocess: %w", err)
	}
	return enriched, input.HookEventName, nil
}

// runPreprocessor threads input through parser.Preprocess. It unpacks
// tool_input from JSON, runs the parser, and repacks the result so downstream
// stages see a Claude-Code-shaped map at tool_input.parsed.
func runPreprocessor(input *envelope.Input) (*envelope.Input, error) {
	base := map[string]any{
		"hook_event_name": input.HookEventName,
		"tool_name":       input.ToolName,
		"session_id":      input.SessionID,
		"cwd":             input.CWD,
	}
	if len(input.ToolInput) > 0 {
		var decoded any
		if err := json.Unmarshal(input.ToolInput, &decoded); err != nil {
			return nil, fmt.Errorf("decode tool_input: %w", err)
		}
		base["tool_input"] = decoded
	}

	enriched, err := parser.Preprocess(input.ToolName, base)
	if err != nil {
		return nil, err
	}

	out := &envelope.Input{
		HookEventName: input.HookEventName,
		ToolName:      input.ToolName,
		ToolResponse:  input.ToolResponse,
		Prompt:        input.Prompt,
		AgentType:     input.AgentType,
		SessionID:     input.SessionID,
		CWD:           input.CWD,
		Signals:       input.Signals,
	}
	if ti, ok := enriched["tool_input"]; ok {
		raw, err := json.Marshal(ti)
		if err != nil {
			return nil, fmt.Errorf("marshal enriched tool_input: %w", err)
		}
		out.ToolInput = raw
	}
	return out, nil
}

// encodeInput converts the enriched envelope.Input into a cue.Value suitable
// for the pipeline. We round-trip through JSON so struct tags drive the field
// names CUE sees — parser.Parsed's lowercase tags align with #Parsed that way.
func encodeInput(input *envelope.Input) (cue.Value, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return cue.Value{}, fmt.Errorf("marshal input: %w", err)
	}
	ctx := cuecontext.New()
	v := ctx.CompileBytes(raw, cue.Filename("input.json"))
	if err := v.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("encode input: %w", err)
	}
	return v, nil
}

// peekHookEventName best-effort extracts the hook_event_name from raw JSON
// before the adapter has parsed it. Used only for fallback rendering when
// parsing itself fails — a missing name means adapter.RenderOutput will emit
// an empty hookEventName, which is still better than aborting.
func peekHookEventName(raw []byte) string {
	var probe struct {
		HookEventName string `json:"hook_event_name"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.HookEventName
}

// fallbackEnvelope is the engine-error safety valve. Default behaviour is
// fail-open (allow) so a broken hook never blocks user work; --fail-closed
// flips to deny and names the underlying error so the user can debug it.
func fallbackEnvelope(cause error, failClosed bool) envelope.OutputEnvelope {
	if failClosed {
		return envelope.OutputEnvelope{
			Category:   envelope.Blocking,
			UserReason: fmt.Sprintf("fas engine error: %v", cause),
		}
	}
	return envelope.OutputEnvelope{Category: envelope.Allowing}
}

// renderExplain emits the filtered explain output to stderr. Fired traces
// come first (one line per match, carrying the rule_id and source file); miss
// diagnostics render afterwards through the shared renderer. The filter is
// applied here so the evaluator's diagnostic lane stays filter-agnostic.
//
// ruleSets supplies the sources the FileCache must be primed from. The CUE
// loader compiles rules inside a synthetic module overlay, so
// token.Pos.Filename() on a diagnostic resolves to that virtual path rather
// than the on-disk absolute path. primeFileCache seeds every spelling the
// renderer may see.
func renderExplain(w io.Writer, filter explainFilter, format outputFormat, color colorMode, matches []evaluator.Match, diags []diag.Diagnostic, ruleSets ...[]config.Rule) {
	if filter == explainFired || filter == explainBoth {
		for _, m := range matches {
			ruleID := ""
			if m.Action != nil {
				ruleID = m.Action.RuleID
			}
			errorf(w, "fired: %s (%s)\n", ruleID, m.Rule.Source)
		}
	}
	if filter == explainMissed || filter == explainBoth {
		writeDiagnostics(w, diags, format, color, ruleSets...)
	}
}

// writeDiagnostics emits diags in the requested format. Text selects a
// palette via paletteFor and writes one Rust-style frame per diagnostic
// (preserving the existing per-diag `rule_id:` header); JSON streams one
// NDJSON object per diag; SARIF emits a single document.
func writeDiagnostics(w io.Writer, diags []diag.Diagnostic, format outputFormat, color colorMode, ruleSets ...[]config.Rule) {
	if len(diags) == 0 {
		return
	}
	switch format {
	case formatJSON:
		_ = diag.RenderJSONStream(w, diags)
	case formatSARIF:
		_, _ = w.Write(diag.RenderSARIF(diags))
	default:
		palette := paletteFor(color, isTTY(os.Stderr), os.Getenv("NO_COLOR"))
		src := primeFileCache(ruleSets...)
		for i := range diags {
			ruleID := ruleIDForDiag(diags[i], ruleSets...)
			errorf(w, "rule_id: %s\n", ruleID)
			_, _ = io.WriteString(w, diag.RenderWithPalette(diags[i], src, palette))
		}
	}
}

// primeFileCache reads every unique rule source file off disk and seeds a
// FileCache under every spelling token.Pos.Filename() may carry for that
// rule: the on-disk absolute path, the bare basename, and the synthetic
// module-root path the CUE loader synthesizes inside its overlay.
func primeFileCache(ruleSets ...[]config.Rule) *diag.FileCache {
	cache := diag.NewFileCache()
	seen := map[string]struct{}{}
	for _, rules := range ruleSets {
		for _, r := range rules {
			path := ruleSourcePath(r.Source)
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			base := filepath.Base(path)
			cache.Set(path, data)
			cache.Set(base, data)
			cache.Set(filepath.Join(config.RulesModuleRoot, base), data)
		}
	}
	return cache
}

// ruleSourcePath strips the `:<fieldname>` suffix LoadRules appends to
// Rule.Source so the remainder is a plain filesystem path.
func ruleSourcePath(source string) string {
	if i := strings.LastIndex(source, ":"); i > 0 {
		return source[:i]
	}
	return source
}

// ruleIDForDiag resolves the rule_id the diagnostic pertains to by matching
// the diagnostic's primary-position filename against the rule's source path.
// Prefers full-path matches so a global and project rule with the same
// basename (deny.cue in both trees) never attributes a diagnostic to the
// wrong rule. Falls back to basename only when no absolute match lands.
// Returns an empty string when no match is found.
func ruleIDForDiag(d diag.Diagnostic, ruleSets ...[]config.Rule) string {
	filename := d.Primary.Pos.Filename()
	if filename == "" {
		return ""
	}
	var baseFallback *config.Rule
	base := filepath.Base(filename)
	slashFilename := filepath.ToSlash(filename)
	for _, rules := range ruleSets {
		for i, r := range rules {
			path := ruleSourcePath(r.Source)
			if filepath.ToSlash(path) == slashFilename {
				return ruleID(r)
			}
			if baseFallback == nil && filepath.Base(path) == base {
				baseFallback = &rules[i]
			}
		}
	}
	if baseFallback != nil {
		return ruleID(*baseFallback)
	}
	return ""
}

// ruleID returns the rule's effect ID when a `then` is attached, empty
// otherwise. Rules without `then` contribute to auditability but do not
// carry a stable identifier for diagnostic attribution.
func ruleID(r config.Rule) string {
	if r.Then != nil {
		return r.Then.RuleID
	}
	return ""
}

// printUsage writes the --help text to w.
func printUsage(w io.Writer) {
	writeUsage(w)
}

// writeUsage is the shared text for --help, -h, and flag parse failures.
// Kept in one place so the three entry points never drift.
func writeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: fas eval [flags]
       fas vet [flags]
       fas explain <rule_id> [flags]

Read a vendor-native hook payload on stdin, evaluate it against the
configured rule sets, and emit a vendor-native response on stdout.

Pipeline: adapter.ParseInput -> parser.Preprocess -> EvaluatePhases (global,
then project) -> synthesis.Synthesize -> adapter.RenderOutput.

Flags:
  --harness <name>        Vendor harness to speak (default: claude).
  --config <path>         Project rules directory (default: `+defaultProjectRulesDir+`).
  --global-config <path>  User-global rules directory (default: ~/`+defaultGlobalRulesSubpath+`).
  --fail-closed           On engine error, emit a Blocking envelope instead
                          of the default Allowing envelope.
  --explain[=MODE]        Emit diagnostics to stderr. MODE is one of
                          fired|missed|both; bare --explain defaults to
                          missed. Omit the flag to disable (zero cost).
  --format <fmt>          Diagnostic output format: text|json|sarif
                          (default: text). JSON emits one object per
                          diagnostic (NDJSON); SARIF emits a single
                          2.1.0 document.
  --color <mode>          Color mode for text diagnostics:
                          auto|always|never (default: auto). Has no
                          effect on --format=json|sarif.
  -h, --help              Show this message and exit.
  --version               Print version and exit.

Subcommands:
  vet                     Validate rule files without evaluating a payload.
                          Loads both rule sets, runs all checks (package
                          clauses, duplicate names, structural lint, schema
                          validation, adapter capabilities), and prints a
                          summary on success. No stdin is read. Exit 0 on
                          valid, 1 on validation error, 2 on usage error.
  explain <rule_id>       Run a single rule (resolved by rule_id across
                          project+global sets; project wins on conflict)
                          against stdin. The rule_id must precede any
                          flags. Implicit --explain=missed; stdin carries
                          the vendor-native payload. Exit 0 on match, 1
                          on no-match (diagnostic on stderr), 2 on engine
                          error.
  explain --code <code>   Print the registered help text for an error
                          code (e.g. E0201) to stdout and exit 0. No
                          stdin is read and no rules are loaded; if
                          --code appears anywhere, it takes precedence
                          over any positional rule_id. Unknown or empty
                          codes exit 2 with a stderr diagnostic.

Environment:
  FAS_EXPLAIN            Truthy (1, true, yes — case-insensitive) enables
                          --explain=missed when --explain is absent. The
                          flag always wins when both are set.
  FAS_FORMAT             Selects the diagnostic output format when
                          --format is absent (text|json|sarif).
  FAS_COLOR              Selects the color mode when --color is absent
                          (auto|always|never). Overrides NO_COLOR.
  NO_COLOR                Community convention: when set (any value),
                          color is disabled. Superseded by --color or
                          FAS_COLOR when those are set.
  FAS_LOG                 Directory for debug payload logs. Set to a
                          directory path to enable; "1" or "true" uses the
                          default (~/.local/state/fas/logs/). Each
                          invocation writes one JSON file recording raw
                          input, loaded rules, matches, and rendered output.
  FAS_LOG_TTL             Max age for debug log files (default: "1h").
                          Files older than this are garbage-collected at
                          the start of each invocation. Accepts Go duration
                          syntax (e.g. 30m, 2h, 24h).

Supported harnesses: claude.
`)
}

// runVet implements `fas vet [flags]`. It loads both rule sets, runs all
// validation checks (package clauses, duplicate names, structural lint,
// schema validation, adapter capability check), and prints a summary of
// loaded rules on success. No stdin is read.
func runVet(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("fas vet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { writeUsage(stderr) }

	defaultGlobal, err := defaultGlobalConfigDir()
	if err != nil {
		defaultGlobal = defaultGlobalRulesSubpath
	}

	harness := "claude"
	projectConfig := defaultProjectRulesDir
	globalConfig := defaultGlobal
	fs.StringVar(&harness, "harness", harness,
		"vendor harness whose hook protocol to speak (e.g. claude)")
	fs.StringVar(&projectConfig, "config", projectConfig,
		"path to the project rules directory")
	fs.StringVar(&globalConfig, "global-config", globalConfig,
		"path to the user-global rules directory")
	var format formatFlag
	var color colorFlag
	fs.Var(&format, "format",
		"diagnostic output format: text|json|sarif (default text)")
	fs.Var(&color, "color",
		"color mode for text diagnostics: auto|always|never (default auto)")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	resolvedFormat, ferr := resolveFormat(format, os.Getenv("FAS_FORMAT"))
	if ferr != nil {
		errorln(stderr, ferr)
		return 2
	}
	if _, cerr := resolveColor(color, os.Getenv("FAS_COLOR"), os.Getenv("NO_COLOR")); cerr != nil {
		errorln(stderr, cerr)
		return 2
	}

	ad, ok := selectAdapter(harness)
	if !ok {
		errorf(stderr, "unknown harness %q; supported: %s\n",
			harness, strings.Join(supportedHarnesses(), ", "))
		return 2
	}

	globalRules, globalErr := loadRulesDir(globalConfig)
	projectRules, projectErr := loadRulesDir(projectConfig)

	loadErr := errors.Join(globalErr, projectErr)
	if loadErr != nil {
		renderVetErrors(stderr, loadErr, resolvedFormat)
		return 1
	}

	if err := checkAdapterCapabilities(ad, globalRules, projectRules); err != nil {
		errorln(stderr, err)
		return 1
	}

	globalIDs := ruleIDs(globalRules)
	projectIDs := ruleIDs(projectRules)
	total := len(globalIDs) + len(projectIDs)

	switch resolvedFormat {
	case formatJSON:
		renderVetSummaryJSON(stdout, globalIDs, projectIDs)
	default:
		errorf(stdout, "ok: %d rules loaded (global: %d, project: %d)\n",
			total, len(globalIDs), len(projectIDs))
		for _, id := range globalIDs {
			errorf(stdout, "  global:  %s\n", id)
		}
		for _, id := range projectIDs {
			errorf(stdout, "  project: %s\n", id)
		}
	}
	return 0
}

// renderVetErrors writes validation errors through the format-aware renderer
// when possible, falling back to plain text for non-diagnostic errors. It
// handles errors.Join aggregates by collecting all embedded DiagErrors.
func renderVetErrors(w io.Writer, err error, format outputFormat) {
	if format == formatText || format == formatUnset {
		errorln(w, err)
		return
	}
	var diags []diag.Diagnostic
	collectDiagErrors(err, &diags)
	if len(diags) == 0 {
		errorln(w, err)
		return
	}
	switch format {
	case formatJSON:
		_ = diag.RenderJSONStream(w, diags)
	case formatSARIF:
		_, _ = w.Write(diag.RenderSARIF(diags))
	}
}

// collectDiagErrors recursively unwraps err (including errors.Join
// aggregates) and appends every embedded diag.Diagnostic to dst.
func collectDiagErrors(err error, dst *[]diag.Diagnostic) {
	if err == nil {
		return
	}
	var de *diag.DiagError
	if errors.As(err, &de) {
		*dst = append(*dst, de.D)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			collectDiagErrors(child, dst)
		}
	}
}

type vetSummary struct {
	Status       string   `json:"status"`
	GlobalRules  []string `json:"global_rules"`
	ProjectRules []string `json:"project_rules"`
	Total        int      `json:"total"`
}

func renderVetSummaryJSON(w io.Writer, globalIDs, projectIDs []string) {
	if globalIDs == nil {
		globalIDs = []string{}
	}
	if projectIDs == nil {
		projectIDs = []string{}
	}
	data, _ := json.Marshal(vetSummary{
		Status:       "ok",
		GlobalRules:  globalIDs,
		ProjectRules: projectIDs,
		Total:        len(globalIDs) + len(projectIDs),
	})
	data = append(data, '\n')
	_, _ = w.Write(data)
}

// ruleIDs extracts the rule_id from each rule's then clause. Rules without
// a then clause are omitted.
func ruleIDs(rules []config.Rule) []string {
	var ids []string
	for _, r := range rules {
		if r.Then != nil && r.Then.RuleID != "" {
			ids = append(ids, r.Then.RuleID)
		}
	}
	return ids
}

// matchSummaries converts evaluator matches into lightweight log entries.
func matchSummaries(matches []evaluator.Match) []debuglog.MatchSummary {
	if len(matches) == 0 {
		return nil
	}
	out := make([]debuglog.MatchSummary, 0, len(matches))
	for _, m := range matches {
		s := debuglog.MatchSummary{Source: m.Rule.Source}
		if m.Action != nil {
			s.RuleID = m.Action.RuleID
			s.Kind = string(m.Action.Kind)
		}
		out = append(out, s)
	}
	return out
}

// exitWithLog records the exit code and closes the recorder, then returns
// the exit code for use in a return statement.
func exitWithLog(rec *debuglog.Recorder, code int) int {
	rec.Close(code)
	return code
}
