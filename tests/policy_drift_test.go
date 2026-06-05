package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bannedInline is maintained alongside the stdlib — an exported matcher added without a key here is a reviewable omission.
var bannedInline = map[string]string{
	// flag/rm.cue
	`^--force(=|$)|^-force(=|$)|^-[a-zA-Z]*f[a-zA-Z]*$`:                "use flag.#hasRmForce",
	`^--recursive(=|$)|^-recursive(=|$)|^-[a-zA-Z]*[rR][a-zA-Z]*$`:     "use flag.#hasRmRecursive",
	`^--interactive(=|$)|^-interactive(=|$)|^-[a-zA-Z]*[iI][a-zA-Z]*$`: "use flag.#hasRmInteractive",
	`^--verbose(=|$)|^-verbose(=|$)|^-[a-zA-Z]*v[a-zA-Z]*$`:            "use flag.#hasRmVerbose",

	// path/path.cue
	`^(/etc|/sys|/proc|/boot|/dev)($|/)`:                           "use path.#systemTarget / path.#hasSystemTarget",
	`(^|[^A-Za-z0-9_])/(etc|sys|proc|boot|dev)(/|$|[^A-Za-z0-9_])`: "use path.#systemInCommand / path.#hasSystemInCommand",

	// command/command.cue
	`=~"^rm\\b"`:    "use command.#isRm",
	`=~"^chmod\\b"`: "use command.#isChmod",
	`=~"^tee\\b"`:   "use command.#isTee",
	`=~"^mv\\b"`:    "use command.#isMv",

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
