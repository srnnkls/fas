// Command quae is the CLI entry point for the Quae policy engine.
//
// quae eval reads a vendor-native hook payload on stdin, evaluates it against
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

	"github.com/srnnkls/quae/internal/adapter"
	"github.com/srnnkls/quae/internal/config"
	"github.com/srnnkls/quae/internal/envelope"
	"github.com/srnnkls/quae/internal/parser"
	"github.com/srnnkls/quae/internal/pipeline"
	"github.com/srnnkls/quae/internal/synthesis"
)

// defaultSizeBudget caps concatenated inject text so a runaway rule cannot
// blow past reasonable hook-response sizes. 8 KiB covers every realistic
// PreToolUse injection; larger payloads are almost certainly bugs.
const defaultSizeBudget = 8192

// defaultProjectRulesDir is the conventional location for per-repo rules.
const defaultProjectRulesDir = ".quae/rules"

// defaultGlobalRulesSubpath is joined onto $HOME/.config to find the
// user-global rules directory when --global-config is omitted.
const defaultGlobalRulesSubpath = ".config/quae/rules"

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr, os.Args[1:]))
}

// run is the in-process entry point exercised by tests. It returns the
// intended process exit code instead of calling os.Exit so test harnesses can
// drive the CLI with bytes.Buffers.
func run(stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	// Drop an optional leading "eval" subcommand so the CLI accepts both
	// `quae eval --harness claude` and `quae --harness claude`. The v0.1
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

	ad, ok := selectAdapter(opts.harness)
	if !ok {
		errorf(stderr, "unknown harness %q; supported: %s\n",
			opts.harness, strings.Join(supportedHarnesses(), ", "))
		return 2
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		errorf(stderr, "read stdin: %v\n", err)
		return 1
	}

	globalRules, err := loadRulesDir(opts.globalConfig)
	if err != nil {
		errorln(stderr, err)
		return 1
	}
	projectRules, err := loadRulesDir(opts.projectConfig)
	if err != nil {
		errorln(stderr, err)
		return 1
	}

	if err := checkAdapterCapabilities(ad, globalRules, projectRules); err != nil {
		errorln(stderr, err)
		return 1
	}

	response, err := evaluate(ad, raw, globalRules, projectRules, opts.failClosed)
	if err != nil {
		errorf(stderr, "render response: %v\n", err)
		return 1
	}

	if _, err := stdout.Write(response); err != nil {
		errorf(stderr, "write stdout: %v\n", err)
		return 1
	}
	return 0
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

// cliOptions bundles the parsed flag state so run stays flat.
type cliOptions struct {
	harness       string
	projectConfig string
	globalConfig  string
	failClosed    bool
}

// parseFlags reads args into a cliOptions. The returned bool is true when the
// user asked for help; callers should print usage and exit 0.
func parseFlags(args []string, stderr io.Writer) (cliOptions, bool, error) {
	fs := flag.NewFlagSet("quae eval", flag.ContinueOnError)
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

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, true, nil
		}
		return opts, false, err
	}
	return opts, false, nil
}

// defaultGlobalConfigDir resolves ~/.config/quae/rules using os.UserHomeDir
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

// evaluate runs the full pipeline and returns the rendered response bytes.
// Engine-level errors (parse, preprocess) are folded into a fail-open/closed
// envelope here; only adapter render failures bubble up as real errors.
func evaluate(
	ad adapter.Adapter,
	raw []byte,
	globalRules, projectRules []config.Rule,
	failClosed bool,
) ([]byte, error) {
	input, hookEventName, engineErr := prepareInput(ad, raw)
	if engineErr != nil {
		out := fallbackEnvelope(engineErr, failClosed)
		return ad.RenderOutput(out, hookEventName)
	}

	cueInput, err := encodeInput(input)
	if err != nil {
		out := fallbackEnvelope(err, failClosed)
		return ad.RenderOutput(out, hookEventName)
	}

	matches, err := pipeline.EvaluatePhases(globalRules, projectRules, cueInput)
	if err != nil {
		out := fallbackEnvelope(err, failClosed)
		return ad.RenderOutput(out, hookEventName)
	}

	out := synthesis.Synthesize(matches, defaultSizeBudget)
	return ad.RenderOutput(out, hookEventName)
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
			UserReason: fmt.Sprintf("quae engine error: %v", cause),
		}
	}
	return envelope.OutputEnvelope{Category: envelope.Allowing}
}

// printUsage writes the --help text to w.
func printUsage(w io.Writer) {
	writeUsage(w)
}

// writeUsage is the shared text for --help, -h, and flag parse failures.
// Kept in one place so the three entry points never drift.
func writeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: quae eval [flags]

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
  -h, --help              Show this message and exit.

Supported harnesses: claude.
`)
}
