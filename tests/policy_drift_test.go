package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bannedInline maps an inline-regex substring that a stdlib helper already
// provides to the helper that should be used instead. A policy .cue containing
// any key has drifted from the canonical catalog.
var bannedInline = map[string]string{
	`(^|[^A-Za-z0-9_])/(etc|sys|proc|boot|dev)(/|$|[^A-Za-z0-9_])`: "use path.#hasSystemInCommand",
	`^-[a-zA-Z]*r[a-zA-Z]*$|^--recursive$`:                         "use flag.#hasRmRecursive",
	`=~"^rm\\b"`:                                                   "use command.#isRm",
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
