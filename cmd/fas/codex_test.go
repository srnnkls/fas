package main

import (
	"slices"
	"strings"
	"testing"
)

func TestCodexHarness(t *testing.T) {
	a, ok := selectAdapter("codex")
	if !ok || a.Name() != "codex" {
		t.Fatal("codex adapter not registered")
	}
	if !slices.Equal(supportedHarnesses(), []string{"claude", "codex", "pi"}) {
		t.Fatal(supportedHarnesses())
	}
	empty := emptyRulesDir(t)
	res := runCLI(t, claudeBashInput("echo hello"), "eval", "--harness", "codex", "--config", empty, "--global-config", empty)
	if res.exit != 0 || string(res.stdout) != "{}" {
		t.Fatalf("exit=%d stdout=%s stderr=%s", res.exit, res.stdout, res.stderr)
	}
	res = runCLI(t, nil, "--help")
	if res.exit != 0 || !strings.Contains(string(res.stdout), "Supported harnesses: claude, codex, pi.") {
		t.Fatalf("help: %s %s", res.stdout, res.stderr)
	}
	res = runCLI(t, claudeBashInput("echo hello"), "eval", "--harness", "bogus")
	if res.exit == 0 || !strings.Contains(string(res.stderr), "claude") || !strings.Contains(string(res.stderr), "codex") {
		t.Fatalf("unknown harness: %s", res.stderr)
	}
	project := writeRuleFiles(t, map[string]string{"modify.cue": modifyRuleSrc})
	res = runCLI(t, claudeBashInput("echo hello"), "eval", "--harness", "codex", "--config", project, "--global-config", empty)
	if res.exit == 0 || !strings.Contains(string(res.stderr), "modify") || !strings.Contains(string(res.stderr), "codex") {
		t.Fatalf("modify accepted: %s", res.stderr)
	}
}
