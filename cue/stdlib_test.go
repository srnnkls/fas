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
	subPkgHook       subPkg = "hook"
	subPkgTool       subPkg = "tool"
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

func TestPath_SystemTarget_Regex(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgPath)

	ok := []string{"/etc/passwd", "/sys/power", "/proc/1/status", "/boot/vmlinuz", "/dev/null"}
	for _, s := range ok {
		t.Run("match/"+s, func(t *testing.T) {
			matchRegexExpectOK(t, ctx, pkg, "systemTarget", s)
		})
	}
	bad := []string{"/home/user", "/tmp/foo", "./etc/foo", "etc/foo", "/var/log"}
	for _, s := range bad {
		t.Run("reject/"+s, func(t *testing.T) {
			matchRegexExpectFail(t, ctx, pkg, "systemTarget", s)
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

	for _, s := range []string{"sudo", "doas", "su"} {
		t.Run("match/"+s, func(t *testing.T) {
			matchRegexExpectOK(t, ctx, pkg, "escalationCommand", s)
		})
	}
	for _, s := range []string{"sudoers", "subarachnoid", "sudoedit", ""} {
		t.Run("reject/"+s, func(t *testing.T) {
			matchRegexExpectFail(t, ctx, pkg, "escalationCommand", s)
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

	for _, s := range []string{"remove", "truncate", "delete", "drop", "destroy"} {
		t.Run("match/"+s, func(t *testing.T) {
			matchRegexExpectOK(t, ctx, pkg, "destructiveAction", s)
		})
	}
	// "rm" is a command name, not a semantic verb — must not match.
	for _, s := range []string{"rm", "psql", "cp", "removed", "removing"} {
		t.Run("reject/"+s, func(t *testing.T) {
			matchRegexExpectFail(t, ctx, pkg, "destructiveAction", s)
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
// tool package — #isBash
// ---------------------------------------------------------------------------

func TestTool_IsBash(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgTool)

	unifyExpectOK(t, ctx, pkg, "isBash",
		`{tool_name: "Bash"}`)
	unifyExpectFail(t, ctx, pkg, "isBash",
		`{tool_name: "Write"}`)
	unifyExpectFail(t, ctx, pkg, "isBash",
		`{tool_name: "Edit"}`)
}

// ---------------------------------------------------------------------------
// flag package — #HasFlagMatching plus rm-specific helpers
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

func TestFlag_HasRmForce_LongForms(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("--force"))
	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("--force=value"))
	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("-force"))
	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("-force=value"))
}

func TestFlag_HasRmForce_ShortCombos(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("-f"))
	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("-rf"))
	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("-vrf"))
	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("-rfi"))

	// Letter-order permutation: 'f' must match regardless of its position.
	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("-fvr"))
	unifyExpectOK(t, ctx, pkg, "HasRmRecursive", flagsInput("-fvr"))
	unifyExpectOK(t, ctx, pkg, "HasRmVerbose", flagsInput("-fvr"))
}

func TestFlag_HasRmForce_RejectsMissingLetter(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectFail(t, ctx, pkg, "HasRmForce", flagsInput("-r"))
	unifyExpectFail(t, ctx, pkg, "HasRmForce", flagsInput("-ri"))
	unifyExpectFail(t, ctx, pkg, "HasRmForce", flagsInput("--recursive"))
}

func TestFlag_HasRmForce_AnchorCorrectness(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectFail(t, ctx, pkg, "HasRmForce", flagsInput("-force-feed"))
	unifyExpectFail(t, ctx, pkg, "HasRmForce", flagsInput("--forceful"))
}

func TestFlag_HasRmForce_RejectsUnknownLetters(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectFail(t, ctx, pkg, "HasRmForce", flagsInput("-x"))
	unifyExpectFail(t, ctx, pkg, "HasRmForce", flagsInput("-xyz"))
	unifyExpectFail(t, ctx, pkg, "HasRmForce", flagsInput("-abc"))
}

func TestFlag_HasRmRecursive(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectOK(t, ctx, pkg, "HasRmRecursive", flagsInput("-r"))
	unifyExpectOK(t, ctx, pkg, "HasRmRecursive", flagsInput("-rf"))
	unifyExpectOK(t, ctx, pkg, "HasRmRecursive", flagsInput("-vrf"))
	unifyExpectOK(t, ctx, pkg, "HasRmRecursive", flagsInput("--recursive"))
	unifyExpectOK(t, ctx, pkg, "HasRmRecursive", flagsInput("--recursive=true"))

	unifyExpectFail(t, ctx, pkg, "HasRmRecursive", flagsInput("-f"))
	unifyExpectFail(t, ctx, pkg, "HasRmRecursive", flagsInput("--force"))
}

func TestFlag_HasRmInteractive(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectOK(t, ctx, pkg, "HasRmInteractive", flagsInput("-i"))
	unifyExpectOK(t, ctx, pkg, "HasRmInteractive", flagsInput("-ri"))
	unifyExpectOK(t, ctx, pkg, "HasRmInteractive", flagsInput("--interactive"))
	unifyExpectFail(t, ctx, pkg, "HasRmInteractive", flagsInput("-f"))
	unifyExpectFail(t, ctx, pkg, "HasRmInteractive", flagsInput("-x"))

	// Two-letter permutation: -if contains both 'i' and 'f'.
	unifyExpectOK(t, ctx, pkg, "HasRmInteractive", flagsInput("-if"))
	unifyExpectOK(t, ctx, pkg, "HasRmForce", flagsInput("-if"))
}

func TestFlag_HasRmVerbose(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	unifyExpectOK(t, ctx, pkg, "HasRmVerbose", flagsInput("-v"))
	unifyExpectOK(t, ctx, pkg, "HasRmVerbose", flagsInput("-vrf"))
	unifyExpectOK(t, ctx, pkg, "HasRmVerbose", flagsInput("--verbose"))
	unifyExpectOK(t, ctx, pkg, "HasRmVerbose", flagsInput("--verbose=1"))
	unifyExpectFail(t, ctx, pkg, "HasRmVerbose", flagsInput("-r"))
}

// AND composition — building a rule condition that requires multiple flag
// constraints at once.
func TestFlag_HasRmForce_AND_Recursive(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	force := lookupDef(t, pkg, "HasRmForce")
	recursive := lookupDef(t, pkg, "HasRmRecursive")
	both := force.Unify(recursive)

	okVal := ctx.CompileString(flagsInput("-rf"), cue.Filename("both-ok.cue"))
	if err := okVal.Err(); err != nil {
		t.Fatalf("compile -rf input: %v", err)
	}
	if err := both.Unify(okVal).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("expected HasRmForce & HasRmRecursive to match -rf, got %v", err)
	}

	failVal := ctx.CompileString(flagsInput("-f"), cue.Filename("both-fail.cue"))
	if err := failVal.Err(); err != nil {
		t.Fatalf("compile -f input: %v", err)
	}
	if err := both.Unify(failVal).Validate(cue.Concrete(false)); err == nil {
		t.Errorf("expected HasRmForce & HasRmRecursive to fail on -f, but it matched")
	}
}

// The shared building block #HasFlagMatching must be usable directly with a
// #re parameter so per-tool flag files can build on it without duplication.
func TestFlag_HasFlagMatching_BuildingBlock(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	cons := lookupDef(t, pkg, "HasFlagMatching")
	withRE := cons.Unify(ctx.CompileString(`{#re: "^--debug$"}`, cue.Filename("re.cue")))
	if err := withRE.Err(); err != nil {
		t.Fatalf("setting #re on #HasFlagMatching errored: %v", err)
	}

	okVal := ctx.CompileString(flagsInput("--debug"), cue.Filename("debug-ok.cue"))
	if err := withRE.Unify(okVal).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("expected #HasFlagMatching{#re:^--debug$} to match --debug, got %v", err)
	}

	failVal := ctx.CompileString(flagsInput("--quiet"), cue.Filename("debug-fail.cue"))
	if err := withRE.Unify(failVal).Validate(cue.Concrete(false)); err == nil {
		t.Errorf("expected #HasFlagMatching{#re:^--debug$} to reject --quiet")
	}
}

// The rm short-letter class is a concrete string constant so regex
// construction is deterministic at parse time.
func TestFlag_RmShortClass_IsConcreteString(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)

	v := lookupDef(t, pkg, "rmShortClass")
	got, err := v.String()
	if err != nil {
		t.Fatalf("#rmShortClass not a concrete string: %v", err)
	}
	for _, want := range []rune{'f', 'r', 'i', 'v'} {
		if !strings.ContainsRune(got, want) {
			t.Errorf("#rmShortClass %q missing letter %q", got, string(want))
		}
	}
}
