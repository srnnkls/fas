package cue_test

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/srnnkls/fas/internal/parser"
)

func TestBashCallSubsumption(t *testing.T) {
	ctx := cuecontext.New()
	bash := loadSubPkg(t, ctx, subPkgBash)
	option := setParam(t, ctx, loadSubPkg(t, ctx, subPkgFlag), "option", `{#spellings: ["--no-verify"]}`)
	git := option.Unify(ctx.CompileString(`{command: "git", subcommand: "commit"}`))
	commit := lookupDef(t, bash, "call").FillPath(cue.MakePath(cue.Def("match")), git)
	worktree := setParam(t, ctx, bash, "call", `{#match: {command: "git", subcommand: "worktree", subcommand_args: ["add", ...]}}`)
	pair := setParam(t, ctx, bash, "argumentPair", `{#first: "--agent", #second: "reviewer"}`)
	peer := lookupDef(t, bash, "call").FillPath(cue.MakePath(cue.Def("match")), pair.Unify(ctx.CompileString(`{command: "peer"}`)))
	for _, tt := range []struct {
		command string
		matcher cue.Value
		want    bool
	}{
		{`git commit --no-verify`, commit, true},
		{`git status; printf '%s' --no-verify; echo commit`, commit, false},
		{`git commit; other --no-verify`, commit, false},
		{`git -C commit status --no-verify`, commit, false},
		{`git worktree add --detach /tmp/review HEAD`, worktree, true},
		{`/usr/bin/git worktree add /tmp/review HEAD`, worktree, true},
		{`env MODE=review git worktree add /tmp/review HEAD`, worktree, true},
		{`env -u TOKEN -C /repo git worktree add /tmp/review HEAD`, worktree, true},
		{`sudo -u root git worktree add /tmp/review HEAD`, worktree, true},
		{`sudo -n env MODE=review git worktree add /tmp/review HEAD`, worktree, true},
		{`git -C /repo 'worktree' 'add' ../topic HEAD`, worktree, true},
		{`git worktree list; git add .`, worktree, false},
		{`git status; printf '%s' 'git worktree add ../topic HEAD'`, worktree, false},
		{`peer --agent reviewer`, peer, true},
		{`peer --agent=reviewer`, peer, true},
		{`peer --agent "$ROLE" reviewer`, peer, false},
		{`peer status; other --agent reviewer`, peer, false},
	} {
		t.Run(tt.command, func(t *testing.T) {
			if err := tt.matcher.Err(); err != nil {
				t.Fatal(err)
			}
			input := ctx.Encode(map[string]any{"tool_input": map[string]any{"parsed": parser.ParseBash(tt.command)}})
			if err := input.Err(); err != nil {
				t.Fatal(err)
			}
			matched := tt.matcher.Subsume(input, cue.Raw(), cue.Final()) == nil
			if matched != tt.want {
				t.Errorf("matched = %v, want %v", matched, tt.want)
			}
		})
	}
}
