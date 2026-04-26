package parser_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/srnnkls/fas/internal/parser"
)

// Canonical command→verb mapping the Bash parser is expected to honor.
// The implementer owns the authoritative table; this list pins the minimum
// set of entries that the rule stdlib will rely on.
//
//   rm        → remove
//   rmdir     → remove
//   git branch -D <ref> → delete   (subcommand-aware)
//   ls        → list
//   echo      → print
//   printf    → print
//   cat       → read
//   mv        → move
//   cp        → copy
//   touch     → create
//   mkdir     → create
//   apt       → install / remove (subcommand-aware — see tests below)
//
// Command names themselves (rm, git, apt, ls, ...) NEVER appear in Actions.

// equateEmpty lets nil and empty slices compare equal — our parser may emit
// either when no actions/targets/flags are present.
var sliceOpts = cmp.Options{cmpopts.EquateEmpty()}

// assertContainsVerb checks the verb is present without constraining ordering
// or extra verbs the implementer might include (e.g. richer mapping tables).
func assertContainsVerb(t *testing.T, label string, got []string, want string) {
	t.Helper()
	if !slices.Contains(got, want) {
		t.Errorf("%s: Actions %v missing expected verb %q", label, got, want)
	}
}

func assertNotContains(t *testing.T, label string, got []string, forbidden ...string) {
	t.Helper()
	for _, bad := range forbidden {
		if slices.Contains(got, bad) {
			t.Errorf("%s: Actions %v must not contain %q (command names are not verbs)", label, got, bad)
		}
	}
}

func TestBash_Empty(t *testing.T) {
	got := parser.ParseBash("")
	if diff := cmp.Diff(parser.Parsed{}, got, sliceOpts); diff != "" {
		t.Fatalf("empty input: expected zero Parsed (-want +got):\n%s", diff)
	}
}

func TestBash_RemoveAction(t *testing.T) {
	got := parser.ParseBash("rm -rf /etc")
	assertContainsVerb(t, "rm -rf /etc", got.Actions, "remove")
	assertNotContains(t, "rm -rf /etc", got.Actions, "rm")

	if diff := cmp.Diff([]string{"/etc"}, got.Targets, sliceOpts); diff != "" {
		t.Errorf("targets (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"-rf"}, got.Flags, sliceOpts); diff != "" {
		t.Errorf("flags (-want +got):\n%s", diff)
	}
}

func TestBash_GitBranchDelete(t *testing.T) {
	got := parser.ParseBash("git branch -D feature")
	assertContainsVerb(t, "git branch -D feature", got.Actions, "delete")
	assertNotContains(t, "git branch -D feature", got.Actions, "git", "branch")

	if !slices.Contains(got.Targets, "feature") {
		t.Errorf("targets %v missing ref %q", got.Targets, "feature")
	}
	if !slices.Contains(got.Flags, "-D") {
		t.Errorf("flags %v missing %q", got.Flags, "-D")
	}
}

func TestBash_NoCommandNameInActions(t *testing.T) {
	// Multiple tools; none of their command names should appear as actions.
	cases := []struct {
		in      string
		forbid  []string
		require string
	}{
		{in: "rm file", forbid: []string{"rm"}, require: "remove"},
		{in: "ls -la", forbid: []string{"ls"}, require: "list"},
		{in: "echo hello", forbid: []string{"echo"}, require: "print"},
		{in: "cat README", forbid: []string{"cat"}, require: "read"},
		{in: "mv a b", forbid: []string{"mv"}, require: "move"},
		{in: "cp src dst", forbid: []string{"cp"}, require: "copy"},
		{in: "mkdir foo", forbid: []string{"mkdir"}, require: "create"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parser.ParseBash(tc.in)
			assertContainsVerb(t, tc.in, got.Actions, tc.require)
			assertNotContains(t, tc.in, got.Actions, tc.forbid...)
		})
	}
}

func TestBash_UnmappedCommand_NoActions(t *testing.T) {
	got := parser.ParseBash("mycustomcmd --foo")
	if len(got.Actions) != 0 {
		t.Errorf("unmapped command must yield empty Actions; got %v", got.Actions)
	}
	if !slices.Contains(got.Flags, "--foo") {
		t.Errorf("expected --foo in Flags; got %v", got.Flags)
	}
}

func TestBash_Flags_RawTokens(t *testing.T) {
	got := parser.ParseBash("rm --force --recursive=true /tmp")
	want := []string{"--force", "--recursive=true"}
	if diff := cmp.Diff(want, got.Flags, sliceOpts); diff != "" {
		t.Errorf("flags must preserve raw tokens (-want +got):\n%s", diff)
	}
	// Flags must never be normalized into Attributes as {"force": true}.
	for _, forbidden := range []string{"force", "recursive"} {
		if _, ok := got.Attributes[forbidden]; ok {
			t.Errorf("Attributes must not normalize flag %q into a boolean/value entry", forbidden)
		}
	}
}

func TestBash_Escalation_Sudo(t *testing.T) {
	got := parser.ParseBash("sudo rm -rf /")

	prefixes, ok := got.Attributes["prefix_commands"].([]string)
	if !ok {
		t.Fatalf("Attributes.prefix_commands missing or wrong type: %#v (type %T)", got.Attributes["prefix_commands"], got.Attributes["prefix_commands"])
	}
	if diff := cmp.Diff([]string{"sudo"}, prefixes); diff != "" {
		t.Errorf("prefix_commands (-want +got):\n%s", diff)
	}

	// The real verb should still be extracted from the escalated command.
	assertContainsVerb(t, "sudo rm -rf /", got.Actions, "remove")
	assertNotContains(t, "sudo rm -rf /", got.Actions, "sudo", "rm")
}

func TestBash_Escalation_Doas(t *testing.T) {
	got := parser.ParseBash("doas apt install curl")
	prefixes, ok := got.Attributes["prefix_commands"].([]string)
	if !ok {
		t.Fatalf("Attributes.prefix_commands missing or wrong type: %#v", got.Attributes["prefix_commands"])
	}
	if diff := cmp.Diff([]string{"doas"}, prefixes); diff != "" {
		t.Errorf("prefix_commands (-want +got):\n%s", diff)
	}
}

func TestBash_Pipeline(t *testing.T) {
	got := parser.ParseBash("ls | grep foo")
	if v, ok := got.Attributes["pipeline"].(bool); !ok || !v {
		t.Errorf("Attributes.pipeline = %#v; want true", got.Attributes["pipeline"])
	}
}

func TestBash_NoPipeline_FlagFalseOrAbsent(t *testing.T) {
	got := parser.ParseBash("ls /tmp")
	if v, ok := got.Attributes["pipeline"]; ok {
		if b, isBool := v.(bool); !isBool || b {
			t.Errorf("Attributes.pipeline for non-pipeline command = %#v; want absent or false", v)
		}
	}
}

func TestBash_Redirections_NotInTargets(t *testing.T) {
	// This is the canonical sdl-mcp false-positive case: /dev/null must not
	// leak into Targets just because it is a path in the command string.
	got := parser.ParseBash("rm /tmp/foo 2>/dev/null")

	if slices.Contains(got.Targets, "/dev/null") {
		t.Errorf("Targets must not contain redirection destination /dev/null; got %v", got.Targets)
	}
	if !slices.Contains(got.Targets, "/tmp/foo") {
		t.Errorf("Targets must contain real target /tmp/foo; got %v", got.Targets)
	}

	redirs, ok := got.Attributes["redirections"].([]string)
	if !ok {
		t.Fatalf("Attributes.redirections missing or wrong type: %#v", got.Attributes["redirections"])
	}
	if !slices.ContainsFunc(redirs, func(s string) bool {
		return s == "2>/dev/null" || s == "2> /dev/null"
	}) {
		t.Errorf("Attributes.redirections must capture 2>/dev/null; got %v", redirs)
	}
}

func TestBash_Subshell(t *testing.T) {
	got := parser.ParseBash("echo $(rm /etc)")
	if v, ok := got.Attributes["subshell"].(bool); !ok || !v {
		t.Errorf("Attributes.subshell = %#v; want true", got.Attributes["subshell"])
	}
}

func TestBash_Malformed_ReturnsErrorOrParseErrorAttribute(t *testing.T) {
	// Unterminated quote. Parser surfaces failures via Attributes.parse_error
	// rather than a returned error, so callers can still consume the partial
	// structural facts. Must never panic.
	got := parser.ParseBash(`rm "unterminated`)
	msg, ok := got.Attributes["parse_error"].(string)
	if !ok || msg == "" {
		t.Errorf("malformed input must surface Attributes.parse_error; got Attributes=%v", got.Attributes)
	}
}

// Table-driven coverage for the core mapping surface. Each row is one Bash
// input; expectations pin the fields rules care about without forbidding the
// implementer from enriching Attributes.
func TestBash_Table(t *testing.T) {
	cases := []struct {
		name        string
		command     string
		wantActions []string // minimum set; ordering not enforced
		wantTargets []string
		wantFlags   []string
		forbidAct   []string // e.g. command names
	}{
		{
			name:        "simple_ls",
			command:     "ls",
			wantActions: []string{"list"},
			wantTargets: nil,
			wantFlags:   nil,
			forbidAct:   []string{"ls"},
		},
		{
			name:        "ls_with_flags_and_target",
			command:     "ls -la /home",
			wantActions: []string{"list"},
			wantTargets: []string{"/home"},
			wantFlags:   []string{"-la"},
			forbidAct:   []string{"ls"},
		},
		{
			name:        "rm_recursive_force",
			command:     "rm -rf /etc",
			wantActions: []string{"remove"},
			wantTargets: []string{"/etc"},
			wantFlags:   []string{"-rf"},
			forbidAct:   []string{"rm"},
		},
		{
			name:        "git_branch_delete",
			command:     "git branch -D feature",
			wantActions: []string{"delete"},
			wantTargets: []string{"feature"},
			wantFlags:   []string{"-D"},
			forbidAct:   []string{"git", "branch"},
		},
		{
			name:        "echo_hello",
			command:     "echo hello",
			wantActions: []string{"print"},
			wantTargets: nil,
			wantFlags:   nil,
			forbidAct:   []string{"echo"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parser.ParseBash(tc.command)
			for _, v := range tc.wantActions {
				assertContainsVerb(t, tc.command, got.Actions, v)
			}
			assertNotContains(t, tc.command, got.Actions, tc.forbidAct...)

			for _, target := range tc.wantTargets {
				if !slices.Contains(got.Targets, target) {
					t.Errorf("targets %v missing %q", got.Targets, target)
				}
			}
			for _, flag := range tc.wantFlags {
				if !slices.Contains(got.Flags, flag) {
					t.Errorf("flags %v missing %q", got.Flags, flag)
				}
			}
		})
	}
}

// TestBash_ForLoop_RmVisible asserts the parser recurses into ForClause bodies
// so destructive verbs nested inside `for ... do ... done` are still extracted.
func TestBash_ForLoop_RmVisible(t *testing.T) {
	got := parser.ParseBash("for f in *; do rm $f; done")
	assertContainsVerb(t, "for f in *; do rm $f; done", got.Actions, "remove")
}

// TestBash_IfBranch_RmVisible asserts the parser recurses into IfClause Then
// branches so verbs hidden behind `if ... then ... fi` are still extracted.
func TestBash_IfBranch_RmVisible(t *testing.T) {
	got := parser.ParseBash("if true; then rm x; fi")
	assertContainsVerb(t, "if true; then rm x; fi", got.Actions, "remove")
}

// TestBash_GitWithGlobalFlag_Subcommand asserts subcommand resolution scans
// past leading global flags (e.g. `git -C /foo`) before looking up the verb.
// The subcommand token itself must not leak into Targets.
func TestBash_GitWithGlobalFlag_Subcommand(t *testing.T) {
	got := parser.ParseBash("git -C /foo branch -D feature")
	assertContainsVerb(t, "git -C /foo branch -D feature", got.Actions, "delete")
	assertNotContains(t, "git -C /foo branch -D feature", got.Actions, "git", "branch")

	if !slices.Contains(got.Targets, "feature") {
		t.Errorf("Targets %v missing %q", got.Targets, "feature")
	}
	if slices.Contains(got.Targets, "branch") {
		t.Errorf("Targets %v must not contain resolved subcommand %q", got.Targets, "branch")
	}
}

// TestBash_DoubleDashEndsFlags asserts POSIX `--` ends flag parsing: tokens
// after `--` are treated as positional, and the literal `--` is dropped.
func TestBash_DoubleDashEndsFlags(t *testing.T) {
	got := parser.ParseBash("ls -- --not-a-flag")
	if !slices.Contains(got.Targets, "--not-a-flag") {
		t.Errorf("Targets %v must contain %q after `--`", got.Targets, "--not-a-flag")
	}
	if slices.Contains(got.Flags, "--not-a-flag") {
		t.Errorf("Flags %v must not contain %q after `--`", got.Flags, "--not-a-flag")
	}
	if slices.Contains(got.Flags, "--") {
		t.Errorf("Flags %v must not contain literal `--`", got.Flags)
	}
}

// TestBash_CmdSubstitution_NotInTargets asserts command substitutions never
// leak into Targets/Flags; Attributes.subshell is the authoritative signal.
func TestBash_CmdSubstitution_NotInTargets(t *testing.T) {
	got := parser.ParseBash("echo $(rm foo)")
	for _, target := range got.Targets {
		if strings.Contains(target, "$(") {
			t.Errorf("Targets %v must not include command substitution token %q", got.Targets, target)
		}
	}
	for _, flag := range got.Flags {
		if strings.Contains(flag, "$(") {
			t.Errorf("Flags %v must not include command substitution token %q", got.Flags, flag)
		}
	}
	if v, ok := got.Attributes["subshell"].(bool); !ok || !v {
		t.Errorf("Attributes.subshell = %#v; want true", got.Attributes["subshell"])
	}
}
