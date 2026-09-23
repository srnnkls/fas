package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSymlinkedRulesDir(t *testing.T) {
	project := writeRuleFiles(t, map[string]string{"system.cue": `package rules

rule: {
	when: {hook_event_name: "PreToolUse", tool_name: "Bash"}
	then: deny: {rule_id: "all-bash", reason: "linked rule"}
}
`})
	link := filepath.Join(t.TempDir(), "rules")
	if err := os.Symlink(project, link); err != nil {
		t.Fatal(err)
	}
	empty := emptyRulesDir(t)
	input := claudeBashInput("ls")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FAS_FOLLOW_SYMLINKS", "")

	res := runCLI(t, input, "eval", "--config", link, "--global-config", empty)
	if res.exit != 0 || strings.Contains(string(res.stdout), "linked rule") {
		t.Fatalf("followed without opt-in: %s", res.stdout)
	}
	if !strings.Contains(string(res.stderr), "warning: skipped symlinked rules directory "+link) {
		t.Fatalf("missing warning: %s", res.stderr)
	}

	res = runCLI(t, input, "eval", "--follow-symlinks", "--config", link, "--global-config", empty)
	if !strings.Contains(string(res.stdout), "linked rule") || strings.Contains(string(res.stderr), "warning") {
		t.Fatalf("flag: stdout=%s stderr=%s", res.stdout, res.stderr)
	}

	settingsDir := filepath.Join(home, ".config", "fas")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "config.cue")
	if err := os.WriteFile(settingsPath, []byte("follow_symlinks: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res = runCLI(t, input, "eval", "--config", link, "--global-config", empty)
	if !strings.Contains(string(res.stdout), "linked rule") {
		t.Fatalf("settings: stdout=%s stderr=%s", res.stdout, res.stderr)
	}
	t.Setenv("FAS_FOLLOW_SYMLINKS", "0")
	res = runCLI(t, input, "eval", "--config", link, "--global-config", empty)
	if strings.Contains(string(res.stdout), "linked rule") {
		t.Fatalf("env did not override settings: %s", res.stdout)
	}
	if err := os.WriteFile(settingsPath, []byte("follow_symlinks: \"yes\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAS_FOLLOW_SYMLINKS", "")
	res = runCLI(t, input, "eval", "--config", link, "--global-config", empty)
	if res.exit != 2 || !strings.Contains(string(res.stderr), settingsPath) {
		t.Fatalf("invalid settings: exit=%d stderr=%s", res.exit, res.stderr)
	}
	res = runCLI(t, nil, "vet", "--config", link, "--global-config", empty)
	if res.exit != 2 || !strings.Contains(string(res.stderr), settingsPath) {
		t.Fatalf("vet invalid settings: exit=%d stderr=%s", res.exit, res.stderr)
	}
	if err := os.WriteFile(settingsPath, []byte("follow_symlinks: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAS_FOLLOW_SYMLINKS", "1")
	res = runCLI(t, input, "eval", "--config", link, "--global-config", empty)
	if !strings.Contains(string(res.stdout), "linked rule") {
		t.Fatalf("env: stdout=%s stderr=%s", res.stdout, res.stderr)
	}
	res = runCLI(t, input, "eval", "--follow-symlinks=false", "--config", link, "--global-config", empty)
	if strings.Contains(string(res.stdout), "linked rule") {
		t.Fatalf("flag did not override env: %s", res.stdout)
	}
}
