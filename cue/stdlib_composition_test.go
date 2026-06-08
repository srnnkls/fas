package cue_test

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// composeChain mirrors destructive_home.cue's when chain:
// hook.#PreToolUse & tool.#Tool.Bash & command.#command{rm} &
// (flag.#hasOption & flag.opt.recursive).
func composeChain(t *testing.T, ctx *cue.Context) cue.Value {
	t.Helper()
	preToolUse := lookupDef(t, loadSubPkg(t, ctx, subPkgHook), "PreToolUse")
	bash := lookupDef(t, loadSubPkg(t, ctx, subPkgTool), "Tool").LookupPath(cue.ParsePath("Bash"))
	if !bash.Exists() {
		t.Fatal("#Tool.Bash not found")
	}
	rm := setParam(t, ctx, loadSubPkg(t, ctx, subPkgCommand), "command", `{#name: "rm"}`)
	flagPkg := loadSubPkg(t, ctx, subPkgFlag)
	hasOption := lookupDef(t, flagPkg, "hasOption")
	recursive := hasOption.Unify(flagPkg.LookupPath(cue.ParsePath("opt")).LookupPath(cue.ParsePath("recursive")))
	if err := recursive.Err(); err != nil {
		t.Fatalf("#hasOption & opt.recursive errored: %v", err)
	}

	chain := preToolUse.Unify(bash).Unify(rm).Unify(recursive)
	if err := chain.Err(); err != nil {
		t.Fatalf("compose 4-way chain errored: %v", err)
	}
	return chain
}

func TestComposition_FourWayChain_MatchesSatisfyingInput(t *testing.T) {
	ctx := cuecontext.New()
	chain := composeChain(t, ctx)

	input := ctx.CompileString(
		`{hook_event_name: "PreToolUse", tool_name: "Bash", tool_input: {parsed: {commands: ["rm"], flags: ["-r", "-f"]}}}`,
		cue.Filename("chain-ok.cue"),
	)
	if err := input.Err(); err != nil {
		t.Fatalf("compile satisfying input: %v", err)
	}
	if err := chain.Unify(input).Validate(cue.Concrete(false)); err != nil {
		t.Errorf("expected 4-way chain to match fully-satisfying input, got: %v", err)
	}
}

// One failing conjunct ⇒ whole chain fails. Each row satisfies the other three
// and breaks exactly one.
func TestComposition_FourWayChain_FailsWhenAnyConjunctFails(t *testing.T) {
	ctx := cuecontext.New()
	chain := composeChain(t, ctx)

	rows := []struct {
		broken string
		input  string
	}{
		{"hook.#PreToolUse", `{hook_event_name: "PostToolUse", tool_name: "Bash", tool_input: {parsed: {commands: ["rm"], flags: ["-r", "-f"]}}}`},
		{"tool.#Tool.Bash", `{hook_event_name: "PreToolUse", tool_name: "Write", tool_input: {parsed: {commands: ["rm"], flags: ["-r", "-f"]}}}`},
		{"command.#command{rm}", `{hook_event_name: "PreToolUse", tool_name: "Bash", tool_input: {parsed: {commands: ["ls"], flags: ["-r", "-f"]}}}`},
		{"flag.#hasOption&opt.recursive", `{hook_event_name: "PreToolUse", tool_name: "Bash", tool_input: {parsed: {commands: ["rm"], flags: ["-f"]}}}`},
	}
	for _, row := range rows {
		t.Run("broken/"+row.broken, func(t *testing.T) {
			input := ctx.CompileString(row.input, cue.Filename("chain-fail.cue"))
			if err := input.Err(); err != nil {
				t.Fatalf("compile input: %v", err)
			}
			if err := chain.Unify(input).Validate(cue.Concrete(false)); err == nil {
				t.Errorf("expected chain to fail when %s is unsatisfied, but it matched", row.broken)
			}
		})
	}
}
