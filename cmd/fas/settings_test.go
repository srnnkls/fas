package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUserSettings(t *testing.T, src string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"FAS_FOLLOW_SYMLINKS", "FAS_EXPLAIN", "FAS_FORMAT", "FAS_COLOR", "NO_COLOR", "FAS_LOG", "FAS_LOG_TTL"} {
		t.Setenv(name, "")
	}
	dir := filepath.Join(home, ".config", "fas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.cue"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestSettingsFile(t *testing.T) {
	project := writeRuleFiles(t, map[string]string{"deny.cue": `package rules

rule: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {rule_id: "from-settings", reason: "settings rules"}
}
`})
	empty := emptyRulesDir(t)
	logs := t.TempDir()
	writeUserSettings(t, `project_rules: "`+project+`"
global_rules: "`+empty+`"
fail_closed: true
explain: "missed"
format: "json"
log: "`+logs+`"
`)

	res := runCLI(t, claudeBashInput("ls"), "eval")
	if res.exit != 0 || !strings.Contains(string(res.stdout), "settings rules") {
		t.Fatalf("project_rules: exit=%d stdout=%s stderr=%s", res.exit, res.stdout, res.stderr)
	}
	if entries, _ := os.ReadDir(logs); len(entries) == 0 {
		t.Fatal("log: no debug log written")
	}

	res = runCLI(t, claudeBashInput("ls"), "eval", "--config", empty)
	if strings.Contains(string(res.stdout), "settings rules") {
		t.Fatalf("--config did not override project_rules: %s", res.stdout)
	}

	res = runCLI(t, []byte("not json"), "eval")
	if !strings.Contains(string(res.stdout), `"permissionDecision":"deny"`) {
		t.Fatalf("fail_closed: %s", res.stdout)
	}
	res = runCLI(t, []byte("not json"), "eval", "--fail-closed=false")
	if strings.Contains(string(res.stdout), `"permissionDecision":"deny"`) {
		t.Fatalf("--fail-closed=false did not override: %s", res.stdout)
	}

	opts, _, err := parseFlags(nil, &strings.Builder{})
	if err != nil || opts.format != formatJSON || !opts.explain.set || opts.explain.value != explainMissed {
		t.Fatalf("format/explain: %+v %v", opts, err)
	}
	t.Setenv("FAS_FORMAT", "sarif")
	t.Setenv("FAS_EXPLAIN", "0")
	opts, _, err = parseFlags(nil, &strings.Builder{})
	if err != nil || opts.format != formatSARIF || opts.explain.set {
		t.Fatalf("env did not override file: %+v %v", opts, err)
	}
}

func TestSettingsFileColor(t *testing.T) {
	writeUserSettings(t, `color: "always"`+"\n")
	opts, _, err := parseFlags(nil, &strings.Builder{})
	if err != nil || opts.color != colorAlways {
		t.Fatalf("color: %+v %v", opts, err)
	}
	t.Setenv("NO_COLOR", "1")
	opts, _, _ = parseFlags(nil, &strings.Builder{})
	if opts.color != colorNever {
		t.Fatalf("NO_COLOR did not override file: %v", opts.color)
	}
	t.Setenv("FAS_COLOR", "always")
	opts, _, _ = parseFlags(nil, &strings.Builder{})
	if opts.color != colorAlways {
		t.Fatalf("FAS_COLOR did not override NO_COLOR: %v", opts.color)
	}
}
