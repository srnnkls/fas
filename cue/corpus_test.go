package cue_test

import (
	"os"
	"strings"
	"testing"
)

// corpusRow is one spec-derived case: an input string and the matcher verdict
// a reviewer expects for it (true = match, false = nomatch).
type corpusRow struct {
	Input string
	Match bool
}

// corpusFiles lists every testdata corpus alongside the matcher it pins.
var corpusFiles = map[string]string{
	"rm_flags.tsv":            "flag.#hasOption & flag.opt.recursive",
	"system_paths.tsv":        "path.#systemTarget",
	"commands.tsv":            "command.#command & {#name: \"rm\"}",
	"destructive_actions.tsv": "action.#destructiveAction",
	"escalation.tsv":          "escalation.#escalationCommand",
}

// loadCorpus reads testdata/<name>, skipping comments and blank lines. A
// literal TAB in the input column is written as the two chars `\t` and
// unescaped here.
func loadCorpus(t *testing.T, name string) []corpusRow {
	t.Helper()

	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read corpus %s: %v", name, err)
	}

	var rows []corpusRow
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		input, expected, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("%s:%d: missing tab separator: %q", name, i+1, line)
		}
		var match bool
		switch expected {
		case "match":
			match = true
		case "nomatch":
			match = false
		default:
			t.Fatalf("%s:%d: unknown expected token %q (want match|nomatch)", name, i+1, expected)
		}
		rows = append(rows, corpusRow{
			Input: strings.ReplaceAll(input, `\t`, "\t"),
			Match: match,
		})
	}
	return rows
}

func TestCorpora_Load(t *testing.T) {
	for name := range corpusFiles {
		rows := loadCorpus(t, name)
		if len(rows) == 0 {
			t.Errorf("%s: corpus is empty", name)
		}
	}

	// `\t` in the input column round-trips to a literal TAB.
	cmds := loadCorpus(t, "commands.tsv")
	var sawTab bool
	for _, r := range cmds {
		if strings.Contains(r.Input, "\t") {
			sawTab = true
			if strings.Contains(r.Input, `\t`) {
				t.Errorf("commands.tsv: input still holds escaped tab: %q", r.Input)
			}
		}
	}
	if !sawTab {
		t.Error("commands.tsv: expected at least one row exercising \\t unescaping")
	}
}
