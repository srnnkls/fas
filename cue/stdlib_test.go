package cue_test

import (
	"io/fs"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"

	fascue "github.com/srnnkls/fas/cue"
)

// stdlibModuleRoot is the synthetic root the sub-package test harness uses
// for CUE's load system. A distinct absolute path keeps the overlay from
// colliding with any real directory in the working tree.
//
// OS-aware: cue/load's overlay requires filepath.IsAbs to return true on
// the host OS. POSIX uses "/__fas_stdlib_test__"; Windows needs a volume
// prefix.
var stdlibModuleRoot = computeStdlibModuleRoot()

func computeStdlibModuleRoot() string {
	if runtime.GOOS != "windows" {
		return "/__fas_stdlib_test__"
	}
	return `C:\__fas_stdlib_test__`
}

// stdlibModulePath is the synthetic module name assigned to the harness
// module. The value is arbitrary — CUE only needs a module prefix so the
// overlay can host sibling sub-packages under `cue.mod/pkg/...`.
const stdlibModulePath = "fas.test/stdlib@v0"

// subPkg identifies one of the shipped sub-packages by its relative path
// under cue/ (e.g. "hook", "flag"). The test harness uses it to build an
// overlay path and to form the import path passed to load.Instances.
type subPkg string

const (
	subPkgCatalog    subPkg = "catalog"
	subPkgHook       subPkg = "hook"
	subPkgTool       subPkg = "tool"
	subPkgCommand    subPkg = "command"
	subPkgPath       subPkg = "path"
	subPkgEscalation subPkg = "escalation"
	subPkgAction     subPkg = "action"
	subPkgFlag       subPkg = "flag"
)

// stdlibOverlay stages every embedded CUE file under the test module's
// `cue.mod/pkg/github.com/srnnkls/fas/cue/` tree so load.Instances can
// resolve each sub-package by its canonical import path.
func stdlibOverlay(t *testing.T) map[string]load.Source {
	t.Helper()

	pkgRoot := filepath.Join(
		stdlibModuleRoot, "cue.mod", "pkg",
		filepath.FromSlash(fascue.StdlibImportPathPrefix),
	)
	overlay := map[string]load.Source{
		filepath.Join(stdlibModuleRoot, "cue.mod", "module.cue"): load.FromString(
			`module: "` + stdlibModulePath + `"` + "\n" + `language: version: "v0.11.0"` + "\n",
		),
		// A placeholder root file so load.Instances has a package to enter
		// even though the harness always selects a sub-package by import
		// path. The content is a trivial, valid CUE struct.
		filepath.Join(stdlibModuleRoot, "root.cue"): load.FromString("package root\n"),
	}

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
			return err
		}
		overlay[filepath.Join(pkgRoot, filepath.FromSlash(p))] = load.FromBytes(data)
		return nil
	})
	if err != nil {
		t.Fatalf("stage stdlib overlay: %v", err)
	}
	return overlay
}

// loadSubPkg compiles the named sub-package in a dedicated cue.Context and
// returns its root value. The harness builds a fresh module overlay for each
// call so sub-package tests stay independent.
func loadSubPkg(t *testing.T, ctx *cue.Context, sp subPkg) cue.Value {
	t.Helper()

	overlay := stdlibOverlay(t)
	importPath := fascue.StdlibImportPathPrefix + "/" + string(sp)

	cfg := &load.Config{
		Dir:        stdlibModuleRoot,
		ModuleRoot: stdlibModuleRoot,
		Overlay:    overlay,
	}
	insts := load.Instances([]string{importPath}, cfg)
	if len(insts) == 0 {
		t.Fatalf("load %s: no instances returned", importPath)
	}
	inst := insts[0]
	if err := inst.Err; err != nil {
		t.Fatalf("load %s: %v", importPath, err)
	}
	v := ctx.BuildInstance(inst)
	if err := v.Err(); err != nil {
		t.Fatalf("build %s: %v", importPath, err)
	}
	return v
}

// lookupDef resolves a top-level #Foo definition on a sub-package instance.
func lookupDef(t *testing.T, pkg cue.Value, name string) cue.Value {
	t.Helper()
	v := pkg.LookupPath(cue.MakePath(cue.Def(name)))
	if err := v.Err(); err != nil {
		t.Fatalf("lookup %s: %v", name, err)
	}
	if !v.Exists() {
		t.Fatalf("definition %s not found", name)
	}
	return v
}

// unifyExpectOK asserts that `constraint & value` validates successfully.
func unifyExpectOK(t *testing.T, ctx *cue.Context, pkg cue.Value, constraintName, valueExpr string) {
	t.Helper()
	cons := lookupDef(t, pkg, constraintName)
	val := ctx.CompileString(valueExpr, cue.Filename("value.cue"))
	if err := val.Err(); err != nil {
		t.Fatalf("value compile error (%s): %v", valueExpr, err)
	}
	out := cons.Unify(val)
	if err := out.Validate(cue.Concrete(false)); err != nil {
		t.Errorf("expected %s to unify with %s, got error: %v",
			constraintName, valueExpr, err)
	}
}

// unifyExpectFail asserts that `constraint & value` fails to validate.
func unifyExpectFail(t *testing.T, ctx *cue.Context, pkg cue.Value, constraintName, valueExpr string) {
	t.Helper()
	cons := lookupDef(t, pkg, constraintName)
	val := ctx.CompileString(valueExpr, cue.Filename("value.cue"))
	if err := val.Err(); err != nil {
		t.Fatalf("value compile error (%s): %v", valueExpr, err)
	}
	out := cons.Unify(val)
	if err := out.Validate(cue.Concrete(false)); err == nil {
		t.Errorf("expected %s to fail for %s, but unification succeeded",
			constraintName, valueExpr)
	}
}

// matchRegexExpectOK / ExpectFail check a regex-shaped constraint (e.g.
// #systemTarget, #escalationCommand, #destructiveAction) against a concrete
// string.
func matchRegexExpectOK(t *testing.T, ctx *cue.Context, pkg cue.Value, constraintName, s string) {
	t.Helper()
	cons := lookupDef(t, pkg, constraintName)
	lit := ctx.CompileString(cueStringLit(s), cue.Filename("literal.cue"))
	if err := lit.Err(); err != nil {
		t.Fatalf("literal compile error: %v", err)
	}
	out := cons.Unify(lit)
	if err := out.Validate(cue.Concrete(true)); err != nil {
		t.Errorf("expected %s to match %q, got: %v", constraintName, s, err)
	}
}

func matchRegexExpectFail(t *testing.T, ctx *cue.Context, pkg cue.Value, constraintName, s string) {
	t.Helper()
	cons := lookupDef(t, pkg, constraintName)
	lit := ctx.CompileString(cueStringLit(s), cue.Filename("literal.cue"))
	if err := lit.Err(); err != nil {
		t.Fatalf("literal compile error: %v", err)
	}
	out := cons.Unify(lit)
	if err := out.Validate(cue.Concrete(true)); err == nil {
		t.Errorf("expected %s to reject %q, but it matched", constraintName, s)
	}
}

// cueStringLit returns a CUE source expression evaluating to a single string
// literal. Backslashes and quotes are escaped conservatively.
func cueStringLit(s string) string {
	return "\"" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + "\""
}

// _ pins runtime.Caller usage so the import survives even if future edits
// remove every runtime reference; kept small so removing it is trivial.
var _ = runtime.Caller

// ---------------------------------------------------------------------------
// path package — #SystemPrefixes, #systemTarget, #hasSystemTarget
// ---------------------------------------------------------------------------

func TestPath_SystemPrefixes_Contents(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgPath)

	list := lookupDef(t, pkg, "SystemPrefixes")
	iter, err := list.List()
	if err != nil {
		t.Fatalf("#SystemPrefixes not a list: %v", err)
	}
	got := map[string]bool{}
	for iter.Next() {
		s, err := iter.Value().String()
		if err != nil {
			t.Fatalf("non-string entry: %v", err)
		}
		got[s] = true
	}
	want := []string{"/etc", "/sys", "/proc", "/boot", "/dev"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("#SystemPrefixes missing %q", w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("#SystemPrefixes has %d entries, want %d", len(got), len(want))
	}
}

// corpusSubtestName keeps the empty-input row addressable as a subtest.
func corpusSubtestName(input string) string {
	if input == "" {
		return "<empty>"
	}
	return input
}

func TestPath_SystemTarget_Regex(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgPath)

	for _, row := range loadCorpus(t, "system_paths.tsv") {
		t.Run(corpusSubtestName(row.Input), func(t *testing.T) {
			if row.Match {
				matchRegexExpectOK(t, ctx, pkg, "systemTarget", row.Input)
			} else {
				matchRegexExpectFail(t, ctx, pkg, "systemTarget", row.Input)
			}
		})
	}
}

func TestPath_HasSystemTarget_AbsolutePrefix(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgPath)

	unifyExpectOK(t, ctx, pkg, "hasSystemTarget",
		`{tool_input: {parsed: {targets: ["/etc/shadow"]}}}`)
	unifyExpectOK(t, ctx, pkg, "hasSystemTarget",
		`{tool_input: {parsed: {targets: ["/home/x", "/sys/power"]}}}`)
}

func TestPath_HasSystemTarget_RejectsRelative(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgPath)

	// This is the sdl-mcp false-positive pattern: "./etc/foo" must NOT
	// be classified as a system path.
	unifyExpectFail(t, ctx, pkg, "hasSystemTarget",
		`{tool_input: {parsed: {targets: ["./etc/foo"]}}}`)
	unifyExpectFail(t, ctx, pkg, "hasSystemTarget",
		`{tool_input: {parsed: {targets: ["/home/alice"]}}}`)
	unifyExpectFail(t, ctx, pkg, "hasSystemTarget",
		`{tool_input: {parsed: {targets: ["/devops"]}}}`)
	unifyExpectFail(t, ctx, pkg, "hasSystemTarget",
		`{tool_input: {parsed: {targets: []}}}`)
}

// ---------------------------------------------------------------------------
// escalation package — #EscalationCommands, #escalationCommand,
// #hasPrivilegeEscalation
// ---------------------------------------------------------------------------

func TestEscalation_Commands_Contents(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgEscalation)

	list := lookupDef(t, pkg, "EscalationCommands")
	iter, err := list.List()
	if err != nil {
		t.Fatalf("#EscalationCommands not a list: %v", err)
	}
	got := map[string]bool{}
	for iter.Next() {
		s, err := iter.Value().String()
		if err != nil {
			t.Fatalf("non-string entry: %v", err)
		}
		got[s] = true
	}
	for _, want := range []string{"sudo", "doas", "su"} {
		if !got[want] {
			t.Errorf("#EscalationCommands missing %q", want)
		}
	}
}

func TestEscalation_Command_Disjunction(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgEscalation)

	for _, row := range loadCorpus(t, "escalation.tsv") {
		t.Run(corpusSubtestName(row.Input), func(t *testing.T) {
			if row.Match {
				matchRegexExpectOK(t, ctx, pkg, "escalationCommand", row.Input)
			} else {
				matchRegexExpectFail(t, ctx, pkg, "escalationCommand", row.Input)
			}
		})
	}
}

func TestEscalation_HasPrivilegeEscalation(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgEscalation)

	unifyExpectOK(t, ctx, pkg, "hasPrivilegeEscalation",
		`{tool_input: {parsed: {attributes: {prefix_commands: ["sudo"]}}}}`)
	unifyExpectOK(t, ctx, pkg, "hasPrivilegeEscalation",
		`{tool_input: {parsed: {attributes: {prefix_commands: ["doas", "env"]}}}}`)
	unifyExpectFail(t, ctx, pkg, "hasPrivilegeEscalation",
		`{tool_input: {parsed: {attributes: {prefix_commands: ["ls"]}}}}`)
	unifyExpectFail(t, ctx, pkg, "hasPrivilegeEscalation",
		`{tool_input: {parsed: {attributes: {prefix_commands: []}}}}`)
}

// ---------------------------------------------------------------------------
// action package — #DestructiveActions, #destructiveAction,
// #hasDestructiveAction
// ---------------------------------------------------------------------------

func TestAction_Destructive_NoCommandNames(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgAction)

	list := lookupDef(t, pkg, "DestructiveActions")
	iter, err := list.List()
	if err != nil {
		t.Fatalf("#DestructiveActions not a list: %v", err)
	}
	got := map[string]bool{}
	for iter.Next() {
		s, err := iter.Value().String()
		if err != nil {
			t.Fatalf("non-string entry: %v", err)
		}
		got[s] = true
	}
	for _, verb := range []string{"remove", "delete", "truncate"} {
		if !got[verb] {
			t.Errorf("#DestructiveActions missing semantic verb %q", verb)
		}
	}
	// Command names must NOT leak into the action vocabulary.
	for _, cmd := range []string{"rm", "psql", "dd"} {
		if got[cmd] {
			t.Errorf("#DestructiveActions contains command name %q (semantic verbs only)", cmd)
		}
	}
}

func TestAction_Destructive_Disjunction(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgAction)

	for _, row := range loadCorpus(t, "destructive_actions.tsv") {
		t.Run(corpusSubtestName(row.Input), func(t *testing.T) {
			if row.Match {
				matchRegexExpectOK(t, ctx, pkg, "destructiveAction", row.Input)
			} else {
				matchRegexExpectFail(t, ctx, pkg, "destructiveAction", row.Input)
			}
		})
	}
}

func TestAction_HasDestructiveAction_VerbsOnly(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgAction)

	unifyExpectOK(t, ctx, pkg, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: ["remove"]}}}`)
	unifyExpectOK(t, ctx, pkg, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: ["read", "truncate"]}}}`)

	// Command names MUST NOT satisfy #hasDestructiveAction.
	unifyExpectFail(t, ctx, pkg, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: ["rm"]}}}`)
	unifyExpectFail(t, ctx, pkg, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: ["ls", "cat"]}}}`)
	unifyExpectFail(t, ctx, pkg, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: []}}}`)
}

// ---------------------------------------------------------------------------
// catalog package — #ToolName, #AgentType, #EventName identities
// ---------------------------------------------------------------------------

func catalogString(t *testing.T, pkg cue.Value, def, member string) string {
	t.Helper()
	v := pkg.LookupPath(cue.MakePath(cue.Def(def))).LookupPath(cue.ParsePath(member))
	s, err := v.String()
	if err != nil {
		t.Fatalf("catalog.#%s.%s: %v", def, member, err)
	}
	return s
}

func TestCatalog_Identities(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgCatalog)

	for _, tc := range []struct{ def, member, want string }{
		{"ToolName", "Bash", "Bash"},
		{"ToolName", "Write", "Write"},
		{"ToolName", "WebFetch", "WebFetch"},
		{"AgentType", "Explore", "Explore"},
		{"AgentType", "GeneralPurpose", "general-purpose"},
		{"EventName", "PreToolUse", "PreToolUse"},
		{"EventName", "SubagentStop", "SubagentStop"},
	} {
		if got := catalogString(t, pkg, tc.def, tc.member); got != tc.want {
			t.Errorf("catalog.#%s.%s = %q, want %q", tc.def, tc.member, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// tool package — #Tool matchers (bound from catalog.#ToolName), #KnownTool
// ---------------------------------------------------------------------------

func TestTool_Tool_Matchers(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgTool)

	toolDef := lookupDef(t, pkg, "Tool")
	bash := toolDef.LookupPath(cue.ParsePath("Bash"))
	if !bash.Exists() {
		t.Fatal("#Tool.Bash not found")
	}

	okVal := ctx.CompileString(`{tool_name: "Bash"}`, cue.Filename("ok.cue"))
	if err := bash.Unify(okVal).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("#Tool.Bash should match Bash, got: %v", err)
	}
	writeVal := ctx.CompileString(`{tool_name: "Write"}`, cue.Filename("write.cue"))
	if err := bash.Unify(writeVal).Validate(cue.Concrete(false)); err == nil {
		t.Error("#Tool.Bash should not match Write")
	}
	write := toolDef.LookupPath(cue.ParsePath("Write"))
	if err := write.Unify(writeVal).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("#Tool.Write should match Write, got: %v", err)
	}
}

func TestTool_KnownTool_Disjunction(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgTool)

	unifyExpectOK(t, ctx, pkg, "KnownTool", `{tool_name: "Bash"}`)
	unifyExpectOK(t, ctx, pkg, "KnownTool", `{tool_name: "WebSearch"}`)
	unifyExpectFail(t, ctx, pkg, "KnownTool", `{tool_name: "DefinitelyNotABuiltin"}`)
}

// ---------------------------------------------------------------------------
// command package — Bash command matchers (rm, tee, …)
// ---------------------------------------------------------------------------

func TestCommand_Matchers(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgCommand)

	unifyExpectOK(t, ctx, pkg, "isTee", `{tool_input: {command: "tee /etc/hosts"}}`)
	unifyExpectOK(t, ctx, pkg, "isMv", `{tool_input: {command: "mv a b"}}`)
}

func TestCommand_IsRm_Corpus(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgCommand)

	for _, row := range loadCorpus(t, "commands.tsv") {
		input := `{tool_input: {command: ` + cueStringLit(row.Input) + `}}`
		t.Run(corpusSubtestName(row.Input), func(t *testing.T) {
			if row.Match {
				unifyExpectOK(t, ctx, pkg, "isRm", input)
			} else {
				unifyExpectFail(t, ctx, pkg, "isRm", input)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// flag package — #hasFlagMatching plus rm-specific helpers
// ---------------------------------------------------------------------------

// flagsInput builds a CUE literal of the shape
//
//	{tool_input: {parsed: {flags: [<tokens...>]}}}
func flagsInput(tokens ...string) string {
	quoted := make([]string, len(tokens))
	for i, tok := range tokens {
		quoted[i] = cueStringLit(tok)
	}
	return "{tool_input: {parsed: {flags: [" + strings.Join(quoted, ", ") + "]}}}"
}

func TestFlag_hasRmForce_LongForms(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("--force"))
	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("--force=value"))
	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("-force"))
	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("-force=value"))
}

func TestFlag_hasRmForce_ShortCombos(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("-f"))
	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("-rf"))
	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("-Rf"))
	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("-vrf"))
	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("-rfi"))

	// Letter-order permutation: 'f' must match regardless of its position.
	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("-fvr"))
	unifyExpectOK(t, ctx, pkg, "hasRmRecursive", flagsInput("-fvr"))
	unifyExpectOK(t, ctx, pkg, "hasRmVerbose", flagsInput("-fvr"))
}

func TestFlag_hasRmForce_RejectsMissingLetter(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectFail(t, ctx, pkg, "hasRmForce", flagsInput("-r"))
	unifyExpectFail(t, ctx, pkg, "hasRmForce", flagsInput("-ri"))
	unifyExpectFail(t, ctx, pkg, "hasRmForce", flagsInput("--recursive"))
}

func TestFlag_hasRmForce_AnchorCorrectness(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectFail(t, ctx, pkg, "hasRmForce", flagsInput("-force-feed"))
	unifyExpectFail(t, ctx, pkg, "hasRmForce", flagsInput("--forceful"))
}

func TestFlag_hasRmForce_RejectsUnknownLetters(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectFail(t, ctx, pkg, "hasRmForce", flagsInput("-x"))
	unifyExpectFail(t, ctx, pkg, "hasRmForce", flagsInput("-xyz"))
	unifyExpectFail(t, ctx, pkg, "hasRmForce", flagsInput("-abc"))
}

func TestFlag_hasRmRecursive(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	for _, row := range loadCorpus(t, "rm_flags.tsv") {
		t.Run(corpusSubtestName(row.Input), func(t *testing.T) {
			if row.Match {
				unifyExpectOK(t, ctx, pkg, "hasRmRecursive", flagsInput(row.Input))
			} else {
				unifyExpectFail(t, ctx, pkg, "hasRmRecursive", flagsInput(row.Input))
			}
		})
	}
}

func TestFlag_hasRmInteractive(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectOK(t, ctx, pkg, "hasRmInteractive", flagsInput("-i"))
	unifyExpectOK(t, ctx, pkg, "hasRmInteractive", flagsInput("-I"))
	unifyExpectOK(t, ctx, pkg, "hasRmInteractive", flagsInput("-ri"))
	unifyExpectOK(t, ctx, pkg, "hasRmInteractive", flagsInput("--interactive"))
	unifyExpectFail(t, ctx, pkg, "hasRmInteractive", flagsInput("-f"))
	unifyExpectFail(t, ctx, pkg, "hasRmInteractive", flagsInput("-x"))

	// Two-letter permutation: -if contains both 'i' and 'f'.
	unifyExpectOK(t, ctx, pkg, "hasRmInteractive", flagsInput("-if"))
	unifyExpectOK(t, ctx, pkg, "hasRmForce", flagsInput("-if"))
}

func TestFlag_hasRmVerbose(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectOK(t, ctx, pkg, "hasRmVerbose", flagsInput("-v"))
	unifyExpectOK(t, ctx, pkg, "hasRmVerbose", flagsInput("-vrf"))
	unifyExpectOK(t, ctx, pkg, "hasRmVerbose", flagsInput("--verbose"))
	unifyExpectOK(t, ctx, pkg, "hasRmVerbose", flagsInput("--verbose=1"))
	unifyExpectFail(t, ctx, pkg, "hasRmVerbose", flagsInput("-r"))
}

// AND composition — building a rule condition that requires multiple flag
// constraints at once.
func TestFlag_hasRmForce_AND_Recursive(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	force := lookupDef(t, pkg, "hasRmForce")
	recursive := lookupDef(t, pkg, "hasRmRecursive")
	both := force.Unify(recursive)

	okVal := ctx.CompileString(flagsInput("-rf"), cue.Filename("both-ok.cue"))
	if err := okVal.Err(); err != nil {
		t.Fatalf("compile -rf input: %v", err)
	}
	if err := both.Unify(okVal).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("expected hasRmForce & hasRmRecursive to match -rf, got %v", err)
	}

	failVal := ctx.CompileString(flagsInput("-f"), cue.Filename("both-fail.cue"))
	if err := failVal.Err(); err != nil {
		t.Fatalf("compile -f input: %v", err)
	}
	if err := both.Unify(failVal).Validate(cue.Concrete(false)); err == nil {
		t.Errorf("expected hasRmForce & hasRmRecursive to fail on -f, but it matched")
	}
}

// The shared building block #hasFlagMatching must be usable directly with a
// #re parameter so per-tool flag files can build on it without duplication.
func TestFlag_hasFlagMatching_BuildingBlock(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	cons := lookupDef(t, pkg, "hasFlagMatching")
	withRE := cons.Unify(ctx.CompileString(`{#re: "^--debug$"}`, cue.Filename("re.cue")))
	if err := withRE.Err(); err != nil {
		t.Fatalf("setting #re on #hasFlagMatching errored: %v", err)
	}

	okVal := ctx.CompileString(flagsInput("--debug"), cue.Filename("debug-ok.cue"))
	if err := withRE.Unify(okVal).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("expected #hasFlagMatching{#re:^--debug$} to match --debug, got %v", err)
	}

	failVal := ctx.CompileString(flagsInput("--quiet"), cue.Filename("debug-fail.cue"))
	if err := withRE.Unify(failVal).Validate(cue.Concrete(false)); err == nil {
		t.Errorf("expected #hasFlagMatching{#re:^--debug$} to reject --quiet")
	}
}

// Cross-subfield composition: command.#isRm touches tool_input.command while
// flag.#hasRmRecursive touches tool_input.parsed.flags. Both matchers must
// stay open at every level of tool_input so they unify under & without a
// "field not allowed" error.
func TestCompose_CommandAndFlag_Unify(t *testing.T) {
	ctx := cuecontext.New()
	cmdPkg := loadSubPkg(t, ctx, subPkgCommand)
	flagPkg := loadSubPkg(t, ctx, subPkgFlag)

	isRm := lookupDef(t, cmdPkg, "isRm")
	hasRecursive := lookupDef(t, flagPkg, "hasRmRecursive")
	composed := isRm.Unify(hasRecursive)
	if err := composed.Err(); err != nil {
		t.Fatalf("command.#isRm & flag.#hasRmRecursive errored: %v", err)
	}

	okVal := ctx.CompileString(
		`{tool_input: {command: "rm -rf x", parsed: {flags: ["-rf"]}}}`,
		cue.Filename("compose-ok.cue"),
	)
	if err := okVal.Err(); err != nil {
		t.Fatalf("compile compose input: %v", err)
	}
	if err := composed.Unify(okVal).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("expected command.#isRm & flag.#hasRmRecursive to match composed input, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// T3 — generic parser-backed matchers: command.#command, command.#subcommand,
// flag.#hasOption, and the flat flag.opt spelling library.
// ---------------------------------------------------------------------------

// commandsInput builds {tool_input: {parsed: {commands: [<tokens...>]}}}.
func commandsInput(tokens ...string) string {
	quoted := make([]string, len(tokens))
	for i, tok := range tokens {
		quoted[i] = cueStringLit(tok)
	}
	return "{tool_input: {parsed: {commands: [" + strings.Join(quoted, ", ") + "]}}}"
}

// subcommandsInput builds a parsed input carrying both commands and subcommands.
func subcommandsInput(commands, subcommands []string) string {
	quote := func(in []string) string {
		out := make([]string, len(in))
		for i, tok := range in {
			out[i] = cueStringLit(tok)
		}
		return strings.Join(out, ", ")
	}
	return "{tool_input: {parsed: {commands: [" + quote(commands) +
		"], subcommands: [" + quote(subcommands) + "]}}}"
}

// setParam unifies a definition with a hidden-param struct literal (e.g.
// `{#name: "rm"}`), returning the parameterized constraint.
func setParam(t *testing.T, ctx *cue.Context, pkg cue.Value, defName, paramExpr string) cue.Value {
	t.Helper()
	cons := lookupDef(t, pkg, defName)
	withParam := cons.Unify(ctx.CompileString(paramExpr, cue.Filename("param.cue")))
	if err := withParam.Err(); err != nil {
		t.Fatalf("setting %s on %s errored: %v", paramExpr, defName, err)
	}
	return withParam
}

// paramExpectOK / paramExpectFail validate a parameterized constraint against a
// parsed input literal through the real Concrete(false) path.
func paramExpectOK(t *testing.T, ctx *cue.Context, cons cue.Value, label, valueExpr string) {
	t.Helper()
	val := ctx.CompileString(valueExpr, cue.Filename("value.cue"))
	if err := val.Err(); err != nil {
		t.Fatalf("value compile error (%s): %v", valueExpr, err)
	}
	if err := cons.Unify(val).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("expected %s to match %s, got error: %v", label, valueExpr, err)
	}
}

func paramExpectFail(t *testing.T, ctx *cue.Context, cons cue.Value, label, valueExpr string) {
	t.Helper()
	val := ctx.CompileString(valueExpr, cue.Filename("value.cue"))
	if err := val.Err(); err != nil {
		t.Fatalf("value compile error (%s): %v", valueExpr, err)
	}
	if err := cons.Unify(val).Validate(cue.Concrete(false)); err == nil {
		t.Errorf("expected %s to fail for %s, but unification succeeded", label, valueExpr)
	}
}

func TestCommand_Command_Matches(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgCommand)

	rm := setParam(t, ctx, pkg, "command", `{#name: "rm"}`)

	paramExpectOK(t, ctx, rm, "#command{rm}", commandsInput("rm"))
	paramExpectFail(t, ctx, rm, "#command{rm}", commandsInput("chmod"))
	paramExpectFail(t, ctx, rm, "#command{rm}", commandsInput())

	// `sudo rm` parses to commands:["rm"]; the match must hold regardless of
	// the raw command string, since only parsed.commands is consulted.
	paramExpectOK(t, ctx, rm, "#command{rm}",
		`{tool_input: {command: "sudo rm -rf /", parsed: {commands: ["rm"]}}}`)
}

func TestCommand_Command_Disjunction(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgCommand)

	rmOrChmod := setParam(t, ctx, pkg, "command", `{#name: "rm" | "chmod"}`)

	paramExpectOK(t, ctx, rmOrChmod, "#command{rm|chmod}", commandsInput("chmod"))
	paramExpectOK(t, ctx, rmOrChmod, "#command{rm|chmod}", commandsInput("rm"))
	paramExpectFail(t, ctx, rmOrChmod, "#command{rm|chmod}", commandsInput("mv"))
}

func TestCommand_Subcommand(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgCommand)

	gitCommitMerge := setParam(t, ctx, pkg, "subcommand",
		`{#of: "git", #name: "commit" | "merge"}`)

	paramExpectOK(t, ctx, gitCommitMerge, "#subcommand{git,commit|merge}",
		subcommandsInput([]string{"git"}, []string{"commit"}))
	paramExpectOK(t, ctx, gitCommitMerge, "#subcommand{git,commit|merge}",
		subcommandsInput([]string{"git"}, []string{"merge"}))
	paramExpectFail(t, ctx, gitCommitMerge, "#subcommand{git,commit|merge}",
		subcommandsInput([]string{"git"}, []string{"push"}))
	// Wrong #of: commands must contain "git", not "docker".
	paramExpectFail(t, ctx, gitCommitMerge, "#subcommand{git,commit|merge}",
		subcommandsInput([]string{"docker"}, []string{"commit"}))
	// No subcommand present at all.
	paramExpectFail(t, ctx, gitCommitMerge, "#subcommand{git,commit|merge}",
		subcommandsInput([]string{"git"}, []string{}))
}

func TestCommand_Subcommand_ValueShadowDenySafe(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgCommand)

	gitAdd := setParam(t, ctx, pkg, "subcommand", `{#of: "git", #name: "add"}`)

	// subcommands is the flat set of all known-subcommand positionals (R13):
	// `git -C commit add <secret>` exposes both "commit" (the shadow value)
	// and "add" (the real subcommand). #subcommand{git,add} MUST still match.
	paramExpectOK(t, ctx, gitAdd, "#subcommand{git,add}",
		subcommandsInput([]string{"git"}, []string{"commit", "add"}))

	// Deny-safe boundary: a shadow value spelling a different known subcommand
	// ("commit") must not satisfy a rule keyed on "add".
	paramExpectFail(t, ctx, gitAdd, "#subcommand{git,add}",
		subcommandsInput([]string{"git"}, []string{"commit"}))
}

func TestFlag_HasOption_SetMembership(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	recursive := setParam(t, ctx, pkg, "hasOption",
		`{#spellings: ["-r", "-R", "--recursive"]}`)

	paramExpectOK(t, ctx, recursive, "#hasOption{recursive}", flagsInput("-R"))
	paramExpectOK(t, ctx, recursive, "#hasOption{recursive}", flagsInput("--recursive"))
	paramExpectOK(t, ctx, recursive, "#hasOption{recursive}", flagsInput("-r"))
	paramExpectFail(t, ctx, recursive, "#hasOption{recursive}", flagsInput("-f"))
	paramExpectFail(t, ctx, recursive, "#hasOption{recursive}", flagsInput())

	// Exact set membership, regex-free: a single-element spelling matches only
	// that exact token. "--no-verify" must not match "-n".
	noVerify := setParam(t, ctx, pkg, "hasOption", `{#spellings: ["--no-verify"]}`)
	paramExpectOK(t, ctx, noVerify, "#hasOption{--no-verify}", flagsInput("--no-verify"))
	paramExpectFail(t, ctx, noVerify, "#hasOption{--no-verify}", flagsInput("-n"))
}

func optSpellings(t *testing.T, optTable cue.Value, name string) map[string]bool {
	t.Helper()
	entry := optTable.LookupPath(cue.ParsePath(name))
	if !entry.Exists() {
		t.Fatalf("opt.%s not found", name)
	}
	spellings := entry.LookupPath(cue.ParsePath("#spellings"))
	iter, err := spellings.List()
	if err != nil {
		t.Fatalf("opt.%s.#spellings not a list: %v", name, err)
	}
	got := map[string]bool{}
	for iter.Next() {
		s, err := iter.Value().String()
		if err != nil {
			t.Fatalf("opt.%s.#spellings non-string entry: %v", name, err)
		}
		got[s] = true
	}
	return got
}

func TestFlag_Opt_Library(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	optTable := pkg.LookupPath(cue.ParsePath("opt"))
	if !optTable.Exists() {
		t.Fatalf("flag.opt library not found")
	}

	for _, tc := range []struct {
		name string
		want []string
	}{
		{"recursive", []string{"-r", "-R", "--recursive"}},
		{"force", []string{"-f", "--force"}},
		{"noVerify", []string{"--no-verify"}},
		{"noVerifyCommit", []string{"--no-verify", "-n"}},
	} {
		got := optSpellings(t, optTable, tc.name)
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("opt.%s.#spellings missing %q", tc.name, w)
			}
		}
		if len(got) != len(tc.want) {
			t.Errorf("opt.%s.#spellings has %d entries, want %d", tc.name, len(got), len(tc.want))
		}
	}

	// R4: noVerifyCommit (commit/merge) accepts -n; noVerify (push, long-only)
	// must reject -n so `git push -n` (dry-run) stays allowed.
	hasOption := lookupDef(t, pkg, "hasOption")

	noVerifyCommit := hasOption.Unify(optTable.LookupPath(cue.ParsePath("noVerifyCommit")))
	if err := noVerifyCommit.Err(); err != nil {
		t.Fatalf("#hasOption & opt.noVerifyCommit errored: %v", err)
	}
	paramExpectOK(t, ctx, noVerifyCommit, "#hasOption & opt.noVerifyCommit", flagsInput("-n"))
	paramExpectOK(t, ctx, noVerifyCommit, "#hasOption & opt.noVerifyCommit", flagsInput("--no-verify"))

	noVerify := hasOption.Unify(optTable.LookupPath(cue.ParsePath("noVerify")))
	if err := noVerify.Err(); err != nil {
		t.Fatalf("#hasOption & opt.noVerify errored: %v", err)
	}
	paramExpectOK(t, ctx, noVerify, "#hasOption & opt.noVerify", flagsInput("--no-verify"))
	paramExpectFail(t, ctx, noVerify, "#hasOption & opt.noVerify", flagsInput("-n"))
}

// Cross-package composition: command.#command reads parsed.commands while
// flag.#hasOption reads parsed.flags. Both must keep `...` at every level so
// they unify under & without a "field not allowed" error.
func TestCompose_CommandSubcommandOption(t *testing.T) {
	ctx := cuecontext.New()
	cmdPkg := loadSubPkg(t, ctx, subPkgCommand)
	flagPkg := loadSubPkg(t, ctx, subPkgFlag)

	git := setParam(t, ctx, cmdPkg, "command", `{#name: "git"}`)

	hasOption := lookupDef(t, flagPkg, "hasOption")
	force := hasOption.Unify(flagPkg.LookupPath(cue.ParsePath("opt")).LookupPath(cue.ParsePath("force")))
	if err := force.Err(); err != nil {
		t.Fatalf("#hasOption & opt.force errored: %v", err)
	}

	composed := git.Unify(force)
	if err := composed.Err(); err != nil {
		t.Fatalf("command.#command{git} & (flag.#hasOption & opt.force) errored: %v", err)
	}

	paramExpectOK(t, ctx, composed, "command{git} & hasOption{force}",
		`{tool_input: {parsed: {commands: ["git"], flags: ["-f"]}}}`)
	paramExpectFail(t, ctx, composed, "command{git} & hasOption{force}",
		`{tool_input: {parsed: {commands: ["git"], flags: []}}}`)
}
