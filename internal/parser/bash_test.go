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
	if diff := cmp.Diff([]string{"-r", "-f"}, got.Flags, sliceOpts); diff != "" {
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

func TestBash_Flags_Debundled(t *testing.T) {
	got := parser.ParseBash("rm --force --recursive=true /tmp")
	// Membership, not order: long opts split at the first `=`, value dropped.
	for _, want := range []string{"--force", "--recursive"} {
		if !slices.Contains(got.Flags, want) {
			t.Errorf("Flags %v missing debundled %q", got.Flags, want)
		}
	}
	if slices.Contains(got.Flags, "--recursive=true") {
		t.Errorf("Flags %v must not contain glued token %q", got.Flags, "--recursive=true")
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
			wantFlags:   []string{"-l", "-a"},
			forbidAct:   []string{"ls"},
		},
		{
			name:        "rm_recursive_force",
			command:     "rm -rf /etc",
			wantActions: []string{"remove"},
			wantTargets: []string{"/etc"},
			wantFlags:   []string{"-r", "-f"},
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

// TestBash_Commands_ResolvedName asserts Parsed.Commands exposes the resolved
// command name (one entry per CallExpr), after escalation strip and after shell
// env-assignments are excluded.
func TestBash_Commands_ResolvedName(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		forbid []string // names that must NOT appear in Commands
	}{
		{in: "rm -rf /etc", want: "rm"},
		{in: "sudo rm -rf /etc", want: "rm", forbid: []string{"sudo"}},
		{in: "FOO=1 rm -rf /etc", want: "rm", forbid: []string{"FOO=1", "FOO"}},
		{in: "git commit", want: "git"},
		{in: "kill 1", want: "kill"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parser.ParseBash(tc.in)
			if !slices.Contains(got.Commands, tc.want) {
				t.Errorf("Commands %v missing resolved command %q", got.Commands, tc.want)
			}
			for _, bad := range tc.forbid {
				if slices.Contains(got.Commands, bad) {
					t.Errorf("Commands %v must not contain %q", got.Commands, bad)
				}
			}
		})
	}
}

// TestBash_Commands_SudoStaysInPrefix asserts that for `sudo rm`, the resolved
// command is "rm" while sudo remains in Attributes.prefix_commands.
func TestBash_Commands_SudoStaysInPrefix(t *testing.T) {
	got := parser.ParseBash("sudo rm -rf /etc")
	if !slices.Contains(got.Commands, "rm") {
		t.Errorf("Commands %v missing %q", got.Commands, "rm")
	}
	if slices.Contains(got.Commands, "sudo") {
		t.Errorf("Commands %v must not contain escalation prefix %q", got.Commands, "sudo")
	}
	prefixes, ok := got.Attributes["prefix_commands"].([]string)
	if !ok || !slices.Contains(prefixes, "sudo") {
		t.Errorf("Attributes.prefix_commands must still contain %q; got %#v", "sudo", got.Attributes["prefix_commands"])
	}
}

// TestBash_Commands_EvalHidesVerb pins residual bypass R9: the real verb hidden
// inside a quoted argument is NOT surfaced; only the literal command "eval" is.
func TestBash_Commands_EvalHidesVerb(t *testing.T) {
	got := parser.ParseBash("eval 'rm -rf ~'")
	if diff := cmp.Diff([]string{"eval"}, got.Commands, sliceOpts); diff != "" {
		t.Errorf("Commands (-want +got):\n%s", diff)
	}
	if slices.Contains(got.Commands, "rm") {
		t.Errorf("Commands %v must not contain verb hidden in quoted string (R9)", got.Commands)
	}
}

// TestBash_Commands_ParseError asserts that on parse_error the early return
// yields empty Commands — the contract the T4 parse-error raw-regex fallback
// depends on (no parsed.commands means callers must fall back to raw matching).
func TestBash_Commands_ParseError(t *testing.T) {
	got := parser.ParseBash(`rm "unterminated`)
	if len(got.Commands) != 0 {
		t.Errorf("parse_error must yield empty Commands; got %v", got.Commands)
	}
	if msg, ok := got.Attributes["parse_error"].(string); !ok || msg == "" {
		t.Errorf("malformed input must surface Attributes.parse_error; got %#v", got.Attributes["parse_error"])
	}
}

// TestBash_Commands_Pipeline asserts each CallExpr in a pipeline contributes one entry to Commands.
func TestBash_Commands_Pipeline(t *testing.T) {
	got := parser.ParseBash("ls | grep foo")
	for _, want := range []string{"ls", "grep"} {
		if !slices.Contains(got.Commands, want) {
			t.Errorf("Commands %v missing %q from pipeline", got.Commands, want)
		}
	}
}

// TestBash_Commands_Compound asserts compound lines are flattened: both command names surface (R10).
func TestBash_Commands_Compound(t *testing.T) {
	got := parser.ParseBash("rm /tmp && echo done")
	for _, want := range []string{"rm", "echo"} {
		if !slices.Contains(got.Commands, want) {
			t.Errorf("Commands %v missing %q from compound line", got.Commands, want)
		}
	}
}

// TestBash_Subcommands_KnownSet asserts Parsed.Subcommands exposes the first
// positional matching the tool's registered subcommand set, robust to
// value-leaking global flags (R1).
func TestBash_Subcommands_KnownSet(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		forbid []string // tokens that must NOT appear in Subcommands
	}{
		{in: "git commit --no-verify", want: "commit"},
		{in: "git -C /repo add .env", want: "add", forbid: []string{"/repo", ".env"}},
		{in: "git -c user.name=x commit", want: "commit", forbid: []string{"user.name=x", "-c"}},
		{in: "git branch -D feature", want: "branch", forbid: []string{"feature"}},
		{in: "apt install curl", want: "install", forbid: []string{"curl"}},
		{in: "apt remove curl", want: "remove", forbid: []string{"curl"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parser.ParseBash(tc.in)
			if !slices.Contains(got.Subcommands, tc.want) {
				t.Errorf("Subcommands %v missing %q", got.Subcommands, tc.want)
			}
			for _, bad := range tc.forbid {
				if slices.Contains(got.Subcommands, bad) {
					t.Errorf("Subcommands %v must not contain %q", got.Subcommands, bad)
				}
			}
		})
	}
}

// TestBash_Subcommands_ValueShadowBypass pins the deny-safe over-match that
// closes the value-shadow bypass: a leaked global-flag value that happens to be
// a registered subcommand ("git -C commit ...") must never hide the real
// subcommand ("add") from Subcommands.
func TestBash_Subcommands_ValueShadowBypass(t *testing.T) {
	got := parser.ParseBash("git -C commit add foo")
	if !slices.Contains(got.Subcommands, "add") {
		t.Errorf("Subcommands %v must contain real subcommand %q (value-shadow bypass)", got.Subcommands, "add")
	}
	if !slices.Contains(got.Subcommands, "commit") {
		t.Errorf("Subcommands %v must contain leaked flag value %q (deny-safe over-match)", got.Subcommands, "commit")
	}
}

// TestBash_Subcommands_Empty asserts commands with no registered subcommands
// (or whose first positional is a target, not a subcommand) yield no subcommand.
func TestBash_Subcommands_Empty(t *testing.T) {
	cases := []struct {
		in     string
		forbid []string
	}{
		{in: "rm /etc", forbid: []string{"/etc"}},
		{in: "kill 10", forbid: []string{"10"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parser.ParseBash(tc.in)
			if len(got.Subcommands) != 0 {
				t.Errorf("Subcommands must be empty for %q; got %v", tc.in, got.Subcommands)
			}
			for _, bad := range tc.forbid {
				if slices.Contains(got.Subcommands, bad) {
					t.Errorf("Subcommands %v must not contain %q", got.Subcommands, bad)
				}
			}
		})
	}
}

// TestBash_Debundle_ShortBundle asserts short flag bundles split per-char.
func TestBash_Debundle_ShortBundle(t *testing.T) {
	got := parser.ParseBash("rm -Rf /etc")
	for _, want := range []string{"-R", "-f"} {
		if !slices.Contains(got.Flags, want) {
			t.Errorf("Flags %v missing debundled %q", got.Flags, want)
		}
	}
	if slices.Contains(got.Flags, "-Rf") {
		t.Errorf("Flags %v must not contain bundled token %q", got.Flags, "-Rf")
	}
}

// TestBash_Debundle_LongOptValue asserts long opts split at the first `=`, with
// the value dropped, including the empty-value case.
func TestBash_Debundle_LongOptValue(t *testing.T) {
	got := parser.ParseBash("rm --recursive=true /tmp")
	if !slices.Contains(got.Flags, "--recursive") {
		t.Errorf("Flags %v missing %q (split at first =)", got.Flags, "--recursive")
	}
	if slices.Contains(got.Flags, "--recursive=true") {
		t.Errorf("Flags %v must not contain glued token %q", got.Flags, "--recursive=true")
	}

	gotEmpty := parser.ParseBash("cmd --opt= foo")
	if !slices.Contains(gotEmpty.Flags, "--opt") {
		t.Errorf("Flags %v missing %q for empty-value long opt", gotEmpty.Flags, "--opt")
	}
}

// TestBash_Debundle_EmptyLongName asserts a long opt with no name before `=`
// ("--=x") is not split into a bare ambiguous "--" flag token.
func TestBash_Debundle_EmptyLongName(t *testing.T) {
	got := parser.ParseBash("cmd --=x")
	if slices.Contains(got.Flags, "--") {
		t.Errorf("Flags %v must not contain bare %q from %q", got.Flags, "--", "--=x")
	}
}

// TestBash_Debundle_AfterDoubleDash asserts `--` ends flags BEFORE debundling:
// a dash-prefixed token after `--` is a target, not a debundled flag.
func TestBash_Debundle_AfterDoubleDash(t *testing.T) {
	got := parser.ParseBash("rm -- -rf foo")
	if diff := cmp.Diff([]string(nil), got.Flags, sliceOpts); diff != "" {
		t.Errorf("Flags must be empty after `--` (-want +got):\n%s", diff)
	}
	if !slices.Contains(got.Targets, "-rf") {
		t.Errorf("Targets %v must contain %q (post-`--` token, not debundled)", got.Targets, "-rf")
	}
	for _, bad := range []string{"-r", "-f"} {
		if slices.Contains(got.Flags, bad) {
			t.Errorf("Flags %v must not debundle post-`--` token into %q", got.Flags, bad)
		}
	}
}

// TestBash_Debundle_SingleCharStays asserts a lone single-char short flag is
// preserved unchanged.
func TestBash_Debundle_SingleCharStays(t *testing.T) {
	got := parser.ParseBash("git branch -D feature")
	if !slices.Contains(got.Flags, "-D") {
		t.Errorf("Flags %v missing single-char flag %q", got.Flags, "-D")
	}
}

// TestBash_Debundle_OverSplitLimitations pins the documented R6 over-split
// behavior so a future correctness fix is a deliberate test change, not a
// silent regression. These are KNOWN LIMITATIONS, not desired semantics.
func TestBash_Debundle_OverSplitLimitations(t *testing.T) {
	// Attached short value over-splits: -mfoo -> -m -f -o (documented false-deny risk).
	attached := parser.ParseBash("cmd -mfoo")
	for _, want := range []string{"-m", "-f"} {
		if !slices.Contains(attached.Flags, want) {
			t.Errorf("Flags %v missing over-split %q for -mfoo (R6)", attached.Flags, want)
		}
	}

	// Single-dash long option splits per-char: -name -> -n -a -m -e (documented).
	longSingle := parser.ParseBash("find -name foo")
	if !slices.Contains(longSingle.Flags, "-n") {
		t.Errorf("Flags %v missing over-split %q for -name (R6)", longSingle.Flags, "-n")
	}
}

// TestBash_Debundle_KillSignalTargetSurvives asserts kill_init policy keeps
// working: command resolves to kill and the PID target survives, regardless of
// how -SIGKILL debundles.
func TestBash_Debundle_KillSignalTargetSurvives(t *testing.T) {
	got := parser.ParseBash("kill -SIGKILL 1")
	if !slices.Contains(got.Commands, "kill") {
		t.Errorf("Commands %v missing %q", got.Commands, "kill")
	}
	if !slices.Contains(got.Targets, "1") {
		t.Errorf("Targets %v must contain PID target %q", got.Targets, "1")
	}
}
