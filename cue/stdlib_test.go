package cue_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// stdlibSources loads the contents of cue/quae.cue and cue/flags/rm.cue as a
// single CUE source string. Both files are authored under `package quae`;
// concatenating their bodies lets the test evaluate all constraints in one
// unified instance without relying on CUE module layout.
func stdlibSources(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	stdlib, err := os.ReadFile(filepath.Join(dir, "quae.cue"))
	if err != nil {
		t.Fatalf("read quae.cue: %v", err)
	}
	flags, err := os.ReadFile(filepath.Join(dir, "flags", "rm.cue"))
	if err != nil {
		t.Fatalf("read flags/rm.cue: %v", err)
	}

	var b strings.Builder
	b.WriteString("package quae\n\n")
	b.WriteString(stripPackage(string(stdlib)))
	b.WriteString("\n")
	b.WriteString(stripPackage(string(flags)))
	return b.String()
}

func stripPackage(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "package ") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func compileStdlib(t *testing.T, ctx *cue.Context) cue.Value {
	t.Helper()
	v := ctx.CompileString(stdlibSources(t), cue.Filename("quae-stdlib.cue"))
	if err := v.Err(); err != nil {
		t.Fatalf("stdlib compile error: %v", err)
	}
	return v
}

// lookupStdlibDef resolves a top-level #Foo definition on the stdlib instance.
func lookupStdlibDef(t *testing.T, stdlib cue.Value, name string) cue.Value {
	t.Helper()
	v := stdlib.LookupPath(cue.MakePath(cue.Def(name)))
	if err := v.Err(); err != nil {
		t.Fatalf("lookup %s: %v", name, err)
	}
	if !v.Exists() {
		t.Fatalf("definition %s not found", name)
	}
	return v
}

// stdlibUnifyExpectOK asserts that `constraint & value` validates successfully.
func stdlibUnifyExpectOK(t *testing.T, ctx *cue.Context, stdlib cue.Value, constraintName, valueExpr string) {
	t.Helper()
	cons := lookupStdlibDef(t, stdlib, constraintName)
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

// stdlibUnifyExpectFail asserts that `constraint & value` fails to validate.
func stdlibUnifyExpectFail(t *testing.T, ctx *cue.Context, stdlib cue.Value, constraintName, valueExpr string) {
	t.Helper()
	cons := lookupStdlibDef(t, stdlib, constraintName)
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

// stdlibMatchRegexExpectOK / ExpectFail check a regex-shaped constraint
// (e.g. #systemTarget, #escalationCommand, #destructiveAction) against a
// concrete string.
func stdlibMatchRegexExpectOK(t *testing.T, ctx *cue.Context, stdlib cue.Value, constraintName, s string) {
	t.Helper()
	cons := lookupStdlibDef(t, stdlib, constraintName)
	lit := ctx.CompileString(cueStringLit(s), cue.Filename("literal.cue"))
	if err := lit.Err(); err != nil {
		t.Fatalf("literal compile error: %v", err)
	}
	out := cons.Unify(lit)
	if err := out.Validate(cue.Concrete(true)); err != nil {
		t.Errorf("expected %s to match %q, got: %v", constraintName, s, err)
	}
}

func stdlibMatchRegexExpectFail(t *testing.T, ctx *cue.Context, stdlib cue.Value, constraintName, s string) {
	t.Helper()
	cons := lookupStdlibDef(t, stdlib, constraintName)
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

// ---------------------------------------------------------------------------
// Layer 1 — value lists
// ---------------------------------------------------------------------------

func TestStdlib_SystemPrefixes_Contents(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	list := lookupStdlibDef(t, stdlib, "SystemPrefixes")
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

func TestStdlib_EscalationCommands_Contents(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	list := lookupStdlibDef(t, stdlib, "EscalationCommands")
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

func TestStdlib_DestructiveActions_NoCommandNames(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	list := lookupStdlibDef(t, stdlib, "DestructiveActions")
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
	// Semantic verbs MUST be present.
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

// ---------------------------------------------------------------------------
// Layer 2 — derived regex / disjunction constraints
// ---------------------------------------------------------------------------

func TestStdlib_SystemTarget_Regex(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	ok := []string{"/etc/passwd", "/sys/power", "/proc/1/status", "/boot/vmlinuz", "/dev/null"}
	for _, s := range ok {
		t.Run("match/"+s, func(t *testing.T) {
			stdlibMatchRegexExpectOK(t, ctx, stdlib, "systemTarget", s)
		})
	}
	bad := []string{"/home/user", "/tmp/foo", "./etc/foo", "etc/foo", "/var/log"}
	for _, s := range bad {
		t.Run("reject/"+s, func(t *testing.T) {
			stdlibMatchRegexExpectFail(t, ctx, stdlib, "systemTarget", s)
		})
	}
}

func TestStdlib_EscalationCommand_Disjunction(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	for _, s := range []string{"sudo", "doas", "su"} {
		t.Run("match/"+s, func(t *testing.T) {
			stdlibMatchRegexExpectOK(t, ctx, stdlib, "escalationCommand", s)
		})
	}
	for _, s := range []string{"sudoers", "subarachnoid", "sudoedit", ""} {
		t.Run("reject/"+s, func(t *testing.T) {
			stdlibMatchRegexExpectFail(t, ctx, stdlib, "escalationCommand", s)
		})
	}
}

func TestStdlib_DestructiveAction_Disjunction(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	for _, s := range []string{"remove", "truncate", "delete", "drop", "destroy"} {
		t.Run("match/"+s, func(t *testing.T) {
			stdlibMatchRegexExpectOK(t, ctx, stdlib, "destructiveAction", s)
		})
	}
	// "rm" is a command name, not a semantic verb — must not match.
	for _, s := range []string{"rm", "psql", "cp", "removed", "removing"} {
		t.Run("reject/"+s, func(t *testing.T) {
			stdlibMatchRegexExpectFail(t, ctx, stdlib, "destructiveAction", s)
		})
	}
}

// ---------------------------------------------------------------------------
// Layer 3 — structural constraints
// ---------------------------------------------------------------------------

func TestStdlib_HasSystemTarget_AbsolutePrefix(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "hasSystemTarget",
		`{tool_input: {parsed: {targets: ["/etc/shadow"]}}}`)
	stdlibUnifyExpectOK(t, ctx, stdlib, "hasSystemTarget",
		`{tool_input: {parsed: {targets: ["/home/x", "/sys/power"]}}}`)
}

func TestStdlib_HasSystemTarget_RejectsRelative(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	// This is the sdl-mcp false-positive pattern: "./etc/foo" must NOT
	// be classified as a system path.
	stdlibUnifyExpectFail(t, ctx, stdlib, "hasSystemTarget",
		`{tool_input: {parsed: {targets: ["./etc/foo"]}}}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "hasSystemTarget",
		`{tool_input: {parsed: {targets: ["/home/alice"]}}}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "hasSystemTarget",
		`{tool_input: {parsed: {targets: []}}}`)
}

func TestStdlib_HasPrivilegeEscalation(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "hasPrivilegeEscalation",
		`{tool_input: {parsed: {attributes: {prefix_commands: ["sudo"]}}}}`)
	stdlibUnifyExpectOK(t, ctx, stdlib, "hasPrivilegeEscalation",
		`{tool_input: {parsed: {attributes: {prefix_commands: ["doas", "env"]}}}}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "hasPrivilegeEscalation",
		`{tool_input: {parsed: {attributes: {prefix_commands: ["ls"]}}}}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "hasPrivilegeEscalation",
		`{tool_input: {parsed: {attributes: {prefix_commands: []}}}}`)
}

func TestStdlib_HasDestructiveAction_VerbsOnly(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: ["remove"]}}}`)
	stdlibUnifyExpectOK(t, ctx, stdlib, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: ["read", "truncate"]}}}`)

	// Command names MUST NOT satisfy #hasDestructiveAction — they don't
	// belong in parsed.actions at all, and even if they did, they aren't
	// members of the semantic-verb vocabulary.
	stdlibUnifyExpectFail(t, ctx, stdlib, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: ["rm"]}}}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: ["ls", "cat"]}}}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "hasDestructiveAction",
		`{tool_input: {parsed: {actions: []}}}`)
}

func TestStdlib_IsPreToolUse(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "isPreToolUse",
		`{hook_event_name: "PreToolUse", tool_name: "Bash"}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "isPreToolUse",
		`{hook_event_name: "PostToolUse"}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "isPreToolUse",
		`{hook_event_name: "UserPromptSubmit"}`)
}

func TestStdlib_IsUserPrompt(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "isUserPrompt",
		`{hook_event_name: "UserPromptSubmit"}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "isUserPrompt",
		`{hook_event_name: "PreToolUse"}`)
}

func TestStdlib_IsBash(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "isBash",
		`{tool_name: "Bash"}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "isBash",
		`{tool_name: "Write"}`)
	stdlibUnifyExpectFail(t, ctx, stdlib, "isBash",
		`{tool_name: "Edit"}`)
}

// ---------------------------------------------------------------------------
// Flag constraints (rm)
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

func TestStdlib_HasRmForce_LongForms(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("--force"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("--force=value"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("-force"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("-force=value"))
}

func TestStdlib_HasRmForce_ShortCombos(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("-f"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("-rf"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("-vrf"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("-rfi"))

	// Letter-order permutation: 'f' must match regardless of its position
	// within the short-combo. A regex that anchored 'f' to a fixed slot
	// (e.g. ^-f[friv]*$) would incorrectly reject -fvr.
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("-fvr"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmRecursive", flagsInput("-fvr"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmVerbose", flagsInput("-fvr"))
}

func TestStdlib_HasRmForce_RejectsMissingLetter(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	// -r contains no 'f' — must NOT be classified as force.
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmForce", flagsInput("-r"))
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmForce", flagsInput("-ri"))
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmForce", flagsInput("--recursive"))
}

func TestStdlib_HasRmForce_AnchorCorrectness(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	// "-force-feed" contains "force" but has extra trailing chars beyond
	// an =-separator. Must not match.
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmForce", flagsInput("-force-feed"))
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmForce", flagsInput("--forceful"))
}

func TestStdlib_HasRmForce_RejectsUnknownLetters(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	// Short-combo must be constrained to the rm short-letter class ("friv").
	// These tokens contain no 'f' AND contain letters not in the class,
	// so they must not sneak past the class filter.
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmForce", flagsInput("-x"))
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmForce", flagsInput("-xyz"))
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmForce", flagsInput("-abc"))
}

func TestStdlib_HasRmRecursive(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmRecursive", flagsInput("-r"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmRecursive", flagsInput("-rf"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmRecursive", flagsInput("-vrf"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmRecursive", flagsInput("--recursive"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmRecursive", flagsInput("--recursive=true"))

	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmRecursive", flagsInput("-f"))
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmRecursive", flagsInput("--force"))
}

func TestStdlib_HasRmInteractive(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmInteractive", flagsInput("-i"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmInteractive", flagsInput("-ri"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmInteractive", flagsInput("--interactive"))
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmInteractive", flagsInput("-f"))
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmInteractive", flagsInput("-x"))

	// Two-letter permutation: -if contains both 'i' and 'f', so both
	// interactive and force constraints must match.
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmInteractive", flagsInput("-if"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmForce", flagsInput("-if"))
}

func TestStdlib_HasRmVerbose(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmVerbose", flagsInput("-v"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmVerbose", flagsInput("-vrf"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmVerbose", flagsInput("--verbose"))
	stdlibUnifyExpectOK(t, ctx, stdlib, "HasRmVerbose", flagsInput("--verbose=1"))
	stdlibUnifyExpectFail(t, ctx, stdlib, "HasRmVerbose", flagsInput("-r"))
}

// AND composition — building a rule condition that requires multiple flag
// constraints at once. Must succeed only when every conjunct holds.
func TestStdlib_HasRmForce_AND_Recursive(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	force := lookupStdlibDef(t, stdlib, "HasRmForce")
	recursive := lookupStdlibDef(t, stdlib, "HasRmRecursive")
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

// The shared building block #HasFlagMatching must exist and be usable directly
// with a #re parameter — confirms the stdlib exports it for per-tool reuse.
func TestStdlib_HasFlagMatching_BuildingBlock(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	cons := lookupStdlibDef(t, stdlib, "HasFlagMatching")
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

// The rm short-letter class is a concrete string constant in the stdlib. The
// design fix explicitly requires a concrete string (not a comprehension) so
// regex construction is deterministic at parse time.
func TestStdlib_RmShortClass_IsConcreteString(t *testing.T) {
	ctx := cuecontext.New()
	stdlib := compileStdlib(t, ctx)

	v := lookupStdlibDef(t, stdlib, "rmShortClass")
	got, err := v.String()
	if err != nil {
		t.Fatalf("#rmShortClass not a concrete string: %v", err)
	}
	// The class must contain every letter corresponding to a shipped
	// #HasRm* constraint.
	for _, want := range []rune{'f', 'r', 'i', 'v'} {
		if !strings.ContainsRune(got, want) {
			t.Errorf("#rmShortClass %q missing letter %q", got, string(want))
		}
	}
}
