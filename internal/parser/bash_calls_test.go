package parser_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/srnnkls/fas/internal/parser"
)

func findCall(calls []parser.Call, command string) (parser.Call, bool) {
	for _, c := range calls {
		if c.Command == command {
			return c, true
		}
	}
	return parser.Call{}, false
}

func TestBash_Calls_GroupTargetsPerCommand(t *testing.T) {
	got := parser.ParseBash("cat README && rm .env")

	cat, ok := findCall(got.Calls, "cat")
	if !ok {
		t.Fatalf("calls %+v missing cat call", got.Calls)
	}
	rm, ok := findCall(got.Calls, "rm")
	if !ok {
		t.Fatalf("calls %+v missing rm call", got.Calls)
	}

	if diff := cmp.Diff([]string{"README"}, cat.Targets); diff != "" {
		t.Errorf("cat call targets (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{".env"}, rm.Targets); diff != "" {
		t.Errorf("rm call targets (-want +got):\n%s", diff)
	}

	for _, c := range got.Calls {
		if c.Command == "cat" {
			for _, target := range c.Targets {
				if target == ".env" {
					t.Errorf("read verb %q must not be grouped with secret target .env", c.Command)
				}
			}
		}
	}
}

func TestBash_Calls_ReadOfSecretGroupsTogether(t *testing.T) {
	got := parser.ParseBash("cat .env")

	if len(got.Calls) != 1 {
		t.Fatalf("want 1 call, got %d: %+v", len(got.Calls), got.Calls)
	}
	c := got.Calls[0]
	if c.Command != "cat" {
		t.Errorf("call command = %q, want cat", c.Command)
	}
	if diff := cmp.Diff([]string{".env"}, c.Targets); diff != "" {
		t.Errorf("call targets (-want +got):\n%s", diff)
	}
	if c.Action != "read" {
		t.Errorf("call action = %q, want read", c.Action)
	}
}

func TestBash_Calls_CarrySubcommandAndFlags(t *testing.T) {
	got := parser.ParseBash("git add -f .env")

	c, ok := findCall(got.Calls, "git")
	if !ok {
		t.Fatalf("calls %+v missing git call", got.Calls)
	}
	if c.Subcommand != "add" {
		t.Errorf("subcommand = %q, want add", c.Subcommand)
	}
	if diff := cmp.Diff([]string{".env"}, c.Targets); diff != "" {
		t.Errorf("git call targets (-want +got):\n%s", diff)
	}
	if !cmp.Equal([]string{"-f"}, c.Flags) {
		t.Errorf("git call flags = %v, want [-f]", c.Flags)
	}
}
