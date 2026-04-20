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

// eventsSources concatenates every CUE file under cue/ that carries the
// `package quae` header — schema.cue, quae.cue, flags/rm.cue, and (when the
// typed-event definitions land) events.cue. Package headers and per-file
// import blocks are stripped so the joined source compiles as a single unit.
//
// The loop is resilient: events.cue is optional during the RED phase. When
// the implementer picks that layout it will be picked up automatically; when
// they add the definitions directly to schema.cue the file list is still
// complete.
func eventsSources(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)

	files := []string{
		filepath.Join(dir, "schema.cue"),
		filepath.Join(dir, "quae.cue"),
		filepath.Join(dir, "events.cue"),
		filepath.Join(dir, "flags", "rm.cue"),
	}

	var b strings.Builder
	b.WriteString("package quae\n\n")
	b.WriteString("import (\n\t\"list\"\n\t\"strings\"\n)\n\n")
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", path, err)
		}
		b.WriteString(stripHeaders(string(src)))
		b.WriteString("\n")
	}
	return b.String()
}

func compileEvents(t *testing.T, ctx *cue.Context) cue.Value {
	t.Helper()
	v := ctx.CompileString(eventsSources(t), cue.Filename("quae-events.cue"))
	if err := v.Err(); err != nil {
		t.Fatalf("events compile error: %v", err)
	}
	return v
}

// eventsLookupDef resolves #Foo on the joined package instance. Unlike the
// stdlib harness this does NOT t.Fatalf on a missing definition — the caller
// inspects .Exists() / .Err() so RED tests can assert "field not found".
func eventsLookupDef(t *testing.T, pkg cue.Value, name string) cue.Value {
	t.Helper()
	return pkg.LookupPath(cue.MakePath(cue.Def(name)))
}

// eventsUnifyOK asserts that `#Def & value` validates cleanly. Fails if the
// definition does not exist (RED) or if unification produces an error.
func eventsUnifyOK(t *testing.T, ctx *cue.Context, pkg cue.Value, defName, valueExpr string) {
	t.Helper()
	cons := eventsLookupDef(t, pkg, defName)
	if !cons.Exists() {
		t.Fatalf("definition #%s not found", defName)
	}
	if err := cons.Err(); err != nil {
		t.Fatalf("#%s has error: %v", defName, err)
	}
	val := ctx.CompileString(valueExpr, cue.Filename("value.cue"))
	if err := val.Err(); err != nil {
		t.Fatalf("value compile error (%s): %v", valueExpr, err)
	}
	if err := cons.Unify(val).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("expected #%s to unify with %s, got error: %v",
			defName, valueExpr, err)
	}
}

// eventsUnifyFail asserts that `#Def & value` fails validation. The check is
// two-step because CUE's `Concrete(false)` tolerates missing required fields
// (they count as "not yet concrete" rather than an error): the direct
// `.Err()` path catches hard conflicts like `"PreToolUse" & "Stop"` or
// `tool_name: "" & !=""`, and the `Concrete(true)` pass catches "required
// field absent from the value" so specs like `#PreToolUse & {hook_event_name:
// "PreToolUse"}` (no tool_name) still flunk as expected.
func eventsUnifyFail(t *testing.T, ctx *cue.Context, pkg cue.Value, defName, valueExpr string) {
	t.Helper()
	cons := eventsLookupDef(t, pkg, defName)
	if !cons.Exists() {
		t.Fatalf("definition #%s not found", defName)
	}
	if err := cons.Err(); err != nil {
		t.Fatalf("#%s has error: %v", defName, err)
	}
	val := ctx.CompileString(valueExpr, cue.Filename("value.cue"))
	if err := val.Err(); err != nil {
		t.Fatalf("value compile error (%s): %v", valueExpr, err)
	}
	unified := cons.Unify(val)
	if err := unified.Err(); err != nil {
		return
	}
	if err := unified.Validate(cue.Concrete(true)); err != nil {
		return
	}
	t.Errorf("expected #%s to fail for %s, but unification succeeded",
		defName, valueExpr)
}

// ---------------------------------------------------------------------------
// #HookEventName — closed disjunction over the six supported hook names
// ---------------------------------------------------------------------------

func TestEvents_HookEventName_AcceptsKnown(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	names := []string{
		"PreToolUse",
		"PostToolUse",
		"UserPromptSubmit",
		"Stop",
		"SubagentStart",
		"Notification",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			lit := cueStringLit(name)
			eventsUnifyOK(t, ctx, pkg, "HookEventName", lit)
		})
	}
}

func TestEvents_HookEventName_RejectsUnknown(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	bad := []string{
		"BogusEvent",
		"",
		"pretooluse",      // lowercase variant of a valid name
		"PreToolUseX",     // prefix-only match must not sneak past
		"Pre_Tool_Use",    // non-canonical separator
		"PostToolUseHook", // suffix drift
	}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			lit := cueStringLit(name)
			eventsUnifyFail(t, ctx, pkg, "HookEventName", lit)
		})
	}
}

// ---------------------------------------------------------------------------
// #Input — hook_event_name must be one of the known names
// ---------------------------------------------------------------------------

func TestEvents_Input_RequiresKnownEventName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	// An #Input with an unknown hook_event_name must fail — today schema.cue
	// types it as `string`, so this currently passes; after #HookEventName is
	// wired in it must reject.
	eventsUnifyFail(t, ctx, pkg, "Input",
		`{hook_event_name: "PreToolUseX"}`)

	// Sanity: a known name still satisfies #Input.
	eventsUnifyOK(t, ctx, pkg, "Input",
		`{hook_event_name: "PreToolUse"}`)
}

// ---------------------------------------------------------------------------
// #PreToolUse — requires non-empty tool_name
// ---------------------------------------------------------------------------

func TestEvents_PreToolUse_RequiresToolName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	// Complete input: event name pinned, tool_name present and non-empty.
	eventsUnifyOK(t, ctx, pkg, "PreToolUse",
		`{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	// Missing tool_name — the typed event must treat it as required.
	eventsUnifyFail(t, ctx, pkg, "PreToolUse",
		`{hook_event_name: "PreToolUse"}`)

	// Empty tool_name — must fail the `string & !=""` constraint.
	eventsUnifyFail(t, ctx, pkg, "PreToolUse",
		`{hook_event_name: "PreToolUse", tool_name: ""}`)
}

func TestEvents_PreToolUse_RejectsWrongEventName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	// The typed event pins hook_event_name: "PreToolUse". Any other value
	// must fail unification — the literal can't be both "PreToolUse" and
	// "Stop" at once.
	eventsUnifyFail(t, ctx, pkg, "PreToolUse",
		`{hook_event_name: "Stop", tool_name: "Bash"}`)
	eventsUnifyFail(t, ctx, pkg, "PreToolUse",
		`{hook_event_name: "PostToolUse", tool_name: "Bash"}`)
}

// ---------------------------------------------------------------------------
// #PostToolUse — same shape as PreToolUse plus tool_response passthrough
// ---------------------------------------------------------------------------

func TestEvents_PostToolUse_RequiresToolName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	eventsUnifyOK(t, ctx, pkg, "PostToolUse",
		`{hook_event_name: "PostToolUse", tool_name: "Bash"}`)

	// PostToolUse-specific field must be carryable through tool_input.
	eventsUnifyOK(t, ctx, pkg, "PostToolUse",
		`{hook_event_name: "PostToolUse", tool_name: "Bash", tool_input: {tool_response: {ok: true}}}`)

	eventsUnifyFail(t, ctx, pkg, "PostToolUse",
		`{hook_event_name: "PostToolUse"}`)
	eventsUnifyFail(t, ctx, pkg, "PostToolUse",
		`{hook_event_name: "PreToolUse", tool_name: "Bash"}`)
}

// ---------------------------------------------------------------------------
// #UserPromptSubmit — requires non-empty prompt
// ---------------------------------------------------------------------------

func TestEvents_UserPromptSubmit_RequiresPrompt(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	eventsUnifyOK(t, ctx, pkg, "UserPromptSubmit",
		`{hook_event_name: "UserPromptSubmit", prompt: "write a test"}`)

	eventsUnifyFail(t, ctx, pkg, "UserPromptSubmit",
		`{hook_event_name: "UserPromptSubmit"}`)
	eventsUnifyFail(t, ctx, pkg, "UserPromptSubmit",
		`{hook_event_name: "UserPromptSubmit", prompt: ""}`)
}

// ---------------------------------------------------------------------------
// #Stop / #SubagentStart / #Notification — event-name pinned, no extras
// ---------------------------------------------------------------------------

func TestEvents_Stop_PinsEventName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	eventsUnifyOK(t, ctx, pkg, "Stop",
		`{hook_event_name: "Stop"}`)
	eventsUnifyFail(t, ctx, pkg, "Stop",
		`{hook_event_name: "PreToolUse"}`)
}

func TestEvents_SubagentStart_PinsEventName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	eventsUnifyOK(t, ctx, pkg, "SubagentStart",
		`{hook_event_name: "SubagentStart"}`)
	eventsUnifyFail(t, ctx, pkg, "SubagentStart",
		`{hook_event_name: "Stop"}`)
}

func TestEvents_Notification_PinsEventName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	eventsUnifyOK(t, ctx, pkg, "Notification",
		`{hook_event_name: "Notification"}`)
	eventsUnifyFail(t, ctx, pkg, "Notification",
		`{hook_event_name: "PreToolUse"}`)
}

// ---------------------------------------------------------------------------
// Composition — typed events combine with the existing #is* / #has* stdlib
// ---------------------------------------------------------------------------

// TestEvents_Composition_WithStdlib confirms that the typed event plays well
// with the Layer-3 structural constraints from quae.cue: a PreToolUse + Bash
// input whose parsed.targets holds an absolute system path matches
// #PreToolUse & #isBash & #hasSystemTarget, while the sdl-mcp false-positive
// pattern (`./etc/foo`) must NOT match even under a typed event.
func TestEvents_Composition_WithStdlib(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	pre := eventsLookupDef(t, pkg, "PreToolUse")
	if !pre.Exists() {
		t.Fatalf("definition #PreToolUse not found")
	}
	bash := eventsLookupDef(t, pkg, "isBash")
	if !bash.Exists() {
		t.Fatalf("definition #isBash not found")
	}
	sysTarget := eventsLookupDef(t, pkg, "hasSystemTarget")
	if !sysTarget.Exists() {
		t.Fatalf("definition #hasSystemTarget not found")
	}

	combined := pre.Unify(bash).Unify(sysTarget)
	// Composed constraints with `list.MatchN(>0, ...)` are unsatisfiable
	// against the default empty list, so `combined.Err()` on its own would
	// fire before any input arrives — the composition is a template, not a
	// concrete value. We evaluate it against concrete fixtures: the positive
	// case supplies a matching targets list, the negative supplies one that
	// the regex rejects.
	okInput := ctx.CompileString(`{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: ["/etc/passwd"]
	}`, cue.Filename("ok.cue"))
	if err := okInput.Err(); err != nil {
		t.Fatalf("compile ok input: %v", err)
	}
	okUnified := combined.Unify(okInput)
	if err := okUnified.Err(); err != nil {
		t.Errorf("expected #PreToolUse & #isBash & #hasSystemTarget to match /etc/passwd input, got: %v", err)
	} else if err := okUnified.Validate(cue.Concrete(true)); err != nil {
		t.Errorf("expected concretised composed match for /etc/passwd input, got: %v", err)
	}

	// sdl-mcp guard: relative path ./etc/foo must NOT satisfy the composed
	// constraint under typed events either.
	missInput := ctx.CompileString(`{
		hook_event_name: "PreToolUse"
		tool_name:       "Bash"
		tool_input: parsed: targets: ["./etc/foo"]
	}`, cue.Filename("miss.cue"))
	if err := missInput.Err(); err != nil {
		t.Fatalf("compile miss input: %v", err)
	}
	missUnified := combined.Unify(missInput)
	if err := missUnified.Err(); err == nil {
		if err := missUnified.Validate(cue.Concrete(true)); err == nil {
			t.Errorf("expected #PreToolUse & #isBash & #hasSystemTarget to REJECT ./etc/foo (relative path)")
		}
	}
}

// ---------------------------------------------------------------------------
// Backward-compat — untyped #isPreToolUse keeps working alongside #PreToolUse
// ---------------------------------------------------------------------------

// TestEvents_IsPreToolUseUntyped_StillMatchesMinimalInput asserts that the
// legacy constraint (hook_event_name equality only) keeps accepting inputs
// without a tool_name — rule authors who haven't migrated must not regress.
func TestEvents_IsPreToolUseUntyped_StillMatchesMinimalInput(t *testing.T) {
	ctx := cuecontext.New()
	pkg := compileEvents(t, ctx)

	eventsUnifyOK(t, ctx, pkg, "isPreToolUse",
		`{hook_event_name: "PreToolUse"}`)
	eventsUnifyOK(t, ctx, pkg, "isPreToolUse",
		`{hook_event_name: "PreToolUse", tool_name: "Bash"}`)
}
