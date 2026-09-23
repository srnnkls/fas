package main

import (
	"strings"
	"testing"
)

func TestPiHarness(t *testing.T) {
	a, ok := selectAdapter("pi")
	if !ok || a.Name() != "pi" {
		t.Fatal("pi adapter not registered")
	}
	empty := emptyRulesDir(t)
	piBash := func(command string) []byte {
		return []byte(`{"hook_event_name":"PreToolUse","tool_name":"bash","tool_input":{"command":"` + command + `"},"tool_call_id":"c1","cwd":"/tmp"}`)
	}
	res := runCLI(t, piBash("echo hello"), "eval", "--harness", "pi", "--config", empty, "--global-config", empty)
	if res.exit != 0 || string(res.stdout) != "{}" {
		t.Fatalf("exit=%d stdout=%s stderr=%s", res.exit, res.stdout, res.stderr)
	}
	res = runCLI(t, piBash("echo hello"), "eval", "--harness", "bogus")
	if res.exit == 0 || !strings.Contains(string(res.stderr), "pi") {
		t.Fatalf("unknown harness: %s", res.stderr)
	}
	project := writeRuleFiles(t, map[string]string{"modify.cue": modifyRuleSrc})
	res = runCLI(t, piBash("echo hello"), "eval", "--harness", "pi", "--config", project, "--global-config", empty)
	if res.exit != 0 || string(res.stdout) != `{"decision":"allow","updated_input":{"command":"ls"}}` {
		t.Fatalf("modify: exit=%d stdout=%s stderr=%s", res.exit, res.stdout, res.stderr)
	}
	policies := "../../tests/policies"
	res = runCLI(t, piBash("cat /etc/shadow"), "eval", "--harness", "pi", "--config", policies, "--global-config", empty)
	if res.exit != 0 || string(res.stdout) != `{"decision":"deny","reason":"System path blocked"}` {
		t.Fatalf("pi bash: exit=%d stdout=%s stderr=%s", res.exit, res.stdout, res.stderr)
	}
	res = runCLI(t, piBash("cat /etc/shadow"), "eval", "--harness", "claude", "--config", policies, "--global-config", empty)
	if res.exit != 0 || strings.Contains(string(res.stdout), "deny") {
		t.Fatalf("lowercase bash matched without pi mapping: %s", res.stdout)
	}
}
