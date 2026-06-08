package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bannedInline is maintained alongside the stdlib — an exported matcher added without a key here is a reviewable omission.
//
// The parse-error fallback regex (^<name>\b) lives in the stdlib
// (command.#commandRobust), not in policy files; this guard only scans
// tests/policies/, so no policy-side carve-out is needed for it.
var bannedInline = map[string]string{
	// flag/options.cue (opt spelling library)
	`^--force(=|$)|^-force(=|$)|^-[a-zA-Z]*f[a-zA-Z]*$`:                "use flag.#hasOption & flag.opt.force",
	`^--recursive(=|$)|^-recursive(=|$)|^-[a-zA-Z]*[rR][a-zA-Z]*$`:     "use flag.#hasOption & flag.opt.recursive",
	`^--interactive(=|$)|^-interactive(=|$)|^-[a-zA-Z]*[iI][a-zA-Z]*$`: "use flag.#hasOption & flag.opt.interactive",
	`^--verbose(=|$)|^-verbose(=|$)|^-[a-zA-Z]*v[a-zA-Z]*$`:            "use flag.#hasOption & flag.opt.verbose",

	// path/path.cue
	`^(/etc|/sys|/proc|/boot|/dev)($|/)`:                           "use path.#systemTarget / path.#hasSystemTarget",
	`(^|[^A-Za-z0-9_])/(etc|sys|proc|boot|dev)(/|$|[^A-Za-z0-9_])`: "use path.#systemInCommand / path.#hasSystemInCommand",

	// command/command.cue (command-name matchers)
	`=~"^rm\\b"`:    "use command.#command / #commandRobust",
	`=~"^chmod\\b"`: "use command.#command / #commandRobust",
	`=~"^tee\\b"`:   "use command.#command / #commandRobust",
	`=~"^mv\\b"`:    "use command.#command / #commandRobust",

	// raw command/subcommand regexes removed by parser-backed matching
	`^git\\s+(commit|merge)`:      "use command.#subcommand & {#of: \"git\", #name: \"commit\" | \"merge\"}",
	`^git\\s+push`:                "use command.#subcommand & {#of: \"git\", #name: \"push\"}",
	`^git\\s+add`:                 "use command.#subcommand & {#of: \"git\", #name: \"add\"}",
	`^kill\\s+(-[A-Z0-9]+\\s+)?1`: "use command.#command & {#name: \"kill\"} with target \"1\"",

	// action/action.cue
	`or(["delete", "drop", "remove", "destroy", "truncate"])`: "use action.#destructiveAction / action.#hasDestructiveAction",

	// escalation/escalation.cue
	`or(["sudo", "doas", "su"])`: "use escalation.#escalationCommand / escalation.#hasPrivilegeEscalation",
}

func policiesDir(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(self), "policies")
}

func TestPoliciesDoNotInlineStdlibRegexes(t *testing.T) {
	dir := policiesDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read policies dir: %v", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		text := string(src)
		for banned, fix := range bannedInline {
			if strings.Contains(text, banned) {
				t.Errorf("%s inlines a stdlib regex (%q): %s", e.Name(), banned, fix)
			}
		}
	}
}
