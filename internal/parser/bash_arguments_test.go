package parser_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/srnnkls/fas/internal/parser"
)

func TestBashLiteralArguments(t *testing.T) {
	for _, tt := range []struct {
		command string
		want    []any
	}{
		{`git 'worktree' "add" '/tmp/a b' HEAD`, []any{"worktree", "add", "/tmp/a b", "HEAD"}},
		{`printf '%s' "$VALUE" $(pwd) '*.go' *.go`, []any{"%s", nil, nil, "*.go", nil}},
		{`printf '%s' a\ b 'a'"b" $'a\tb'`, []any{"%s", "a b", "ab", "a\tb"}},
		{`printf '%s' ~/a /tmp/a`, []any{"%s", nil, "/tmp/a"}},
	} {
		t.Run(tt.command, func(t *testing.T) {
			got := parser.ParseBash(tt.command)
			if diff := cmp.Diff(tt.want, got.Calls[0].Arguments); diff != "" {
				t.Errorf("arguments (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBashGitSubcommandArguments(t *testing.T) {
	for _, tt := range []struct {
		command string
		want    string
		args    []any
	}{
		{`git worktree add ../topic HEAD`, "worktree", []any{"add", "../topic", "HEAD"}},
		{`git -C commit -c advice.foo=false worktree add ../topic HEAD`, "worktree", []any{"add", "../topic", "HEAD"}},
		{`git --git-dir=/repo/.git worktreeinclude apply`, "worktreeinclude", []any{"apply"}},
		{`git -C "$ROOT" worktree list`, "worktree", []any{"list"}},
		{`git "$COMMAND" add .`, "", nil},
		{`git --version`, "", nil},
	} {
		t.Run(tt.command, func(t *testing.T) {
			got := parser.ParseBash(tt.command).Calls[0]
			if got.Subcommand != tt.want {
				t.Errorf("subcommand = %q, want %q", got.Subcommand, tt.want)
			}
			if diff := cmp.Diff(tt.args, got.SubcommandArgs); diff != "" {
				t.Errorf("subcommand args (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBashArgumentPairs(t *testing.T) {
	for _, command := range []string{`peer --agent reviewer`, `peer --agent=reviewer`, `peer '--agent' 'reviewer'`} {
		got := parser.ParseBash(command).Calls[0].ArgumentPairs
		want := parser.ArgumentPair{First: "--agent", Second: "reviewer"}
		found := false
		for _, pair := range got {
			if pair == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: missing %v in %v", command, want, got)
		}
	}
	got := parser.ParseBash(`peer --agent "$ROLE" reviewer`).Calls[0].ArgumentPairs
	if len(got) != 0 {
		t.Errorf("dynamic argument must interrupt adjacency: %v", got)
	}
}
