package cue_test

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// eventsLookupDef resolves #Foo on the hook sub-package instance. Unlike
// stdlib lookups this does NOT t.Fatalf on a missing definition — callers
// inspect .Exists() / .Err() so specific assertions can still fire when a
// definition is absent.
func eventsLookupDef(t *testing.T, pkg cue.Value, name string) cue.Value {
	t.Helper()
	return pkg.LookupPath(cue.MakePath(cue.Def(name)))
}

// eventsUnifyOK asserts that `#Def & value` validates cleanly. Fails if the
// definition does not exist or unification produces an error.
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
	pkg := loadSubPkg(t, ctx, subPkgHook)

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
	pkg := loadSubPkg(t, ctx, subPkgHook)

	bad := []string{
		"BogusEvent",
		"",
		"pretooluse",
		"PreToolUseX",
		"Pre_Tool_Use",
		"PostToolUseHook",
	}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			lit := cueStringLit(name)
			eventsUnifyFail(t, ctx, pkg, "HookEventName", lit)
		})
	}
}

// ---------------------------------------------------------------------------
// #PreToolUse — requires non-empty tool_name
// ---------------------------------------------------------------------------

func TestEvents_PreToolUse_RequiresToolName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgHook)

	eventsUnifyOK(t, ctx, pkg, "PreToolUse",
		`{hook_event_name: "PreToolUse", tool_name: "Bash"}`)

	eventsUnifyFail(t, ctx, pkg, "PreToolUse",
		`{hook_event_name: "PreToolUse"}`)

	eventsUnifyFail(t, ctx, pkg, "PreToolUse",
		`{hook_event_name: "PreToolUse", tool_name: ""}`)
}

func TestEvents_PreToolUse_RejectsWrongEventName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgHook)

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
	pkg := loadSubPkg(t, ctx, subPkgHook)

	eventsUnifyOK(t, ctx, pkg, "PostToolUse",
		`{hook_event_name: "PostToolUse", tool_name: "Bash"}`)

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
	pkg := loadSubPkg(t, ctx, subPkgHook)

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
	pkg := loadSubPkg(t, ctx, subPkgHook)

	eventsUnifyOK(t, ctx, pkg, "Stop",
		`{hook_event_name: "Stop"}`)
	eventsUnifyFail(t, ctx, pkg, "Stop",
		`{hook_event_name: "PreToolUse"}`)
}

func TestEvents_SubagentStart_PinsEventName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgHook)

	eventsUnifyOK(t, ctx, pkg, "SubagentStart",
		`{hook_event_name: "SubagentStart"}`)
	eventsUnifyFail(t, ctx, pkg, "SubagentStart",
		`{hook_event_name: "Stop"}`)
}

func TestEvents_Notification_PinsEventName(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgHook)

	eventsUnifyOK(t, ctx, pkg, "Notification",
		`{hook_event_name: "Notification"}`)
	eventsUnifyFail(t, ctx, pkg, "Notification",
		`{hook_event_name: "PreToolUse"}`)
}
