package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type coverage struct {
	deny  []string
	allow string
}

// policyCoverage pins each shipped rule_id (tests/policies/*.cue) to the scrut
// blocks in tests/policies.md that exercise it: ≥1 deny case and the near-miss
// allow that shares the trigger but lacks the deny condition (INV-7). A rule_id
// without an entry here is a reported gap, so adding a policy forces coverage.
var policyCoverage = map[string]coverage{
	"system-path": {
		deny:  []string{"Blocks rm -rf /etc/passwd", "Blocks cat /etc/shadow", "Blocks rm -rf /sys/power"},
		allow: "Allows rm -rf /devops (prefix is not a complete path component)",
	},
	"system-path-command": {
		deny:  []string{"Blocks for-loop rm under /etc"},
		allow: "Allows npm install && npm test",
	},
	"git-no-verify": {
		deny:  []string{"Blocks git commit --no-verify", "Blocks git commit -n (short form of --no-verify)"},
		allow: "Allows normal git commit",
	},
	"git-push-no-verify": {
		deny:  []string{"Blocks git push --no-verify"},
		allow: "Allows git push -n (dry-run, not the --no-verify bypass)",
	},
	"destructive-home": {
		deny:  []string{"Blocks rm -rf $HOME", "Blocks rm -rf ~", "Blocks rm -R ~", "Blocks rm --recursive=true ~", "Blocks rm -rd ~"},
		allow: "Allows rm -rf ./node_modules",
	},
	"secret-files": {
		deny:  []string{"Blocks git add .env", "Blocks git add credentials.json", "Blocks git add id_rsa"},
		allow: "Allows git add src/main.py",
	},
	"kill-init": {
		deny:  []string{"Blocks kill -9 1 (PID 1 / first disjunct)", "Blocks killall -9 systemd (second disjunct)"},
		allow: "Allows kill 1234 (ordinary PID, matches neither disjunct)",
	},
	"chmod-runtime": {
		deny:  []string{"Blocks chmod 777 /run/docker.sock", "Blocks chmod o+w /run/systemd/private"},
		allow: "Allows chmod +x ./scripts/deploy.sh",
	},
	"mv-var-log": {
		deny:  []string{"Blocks mv /var/log/auth.log /tmp/hidden.log", "Blocks mv /var/log/syslog /var/log/syslog.bak (rotation bypass)"},
		allow: "Allows mv ./logs/debug.log ./archive/debug.log",
	},
	"tee-system-path": {
		deny:  []string{"Blocks tee /etc/sudoers.d/override", "Blocks tee -a /etc/cron.d/task"},
		allow: "Allows tee ./build.log",
	},
	"universe-or-doc-tools": {
		deny:  []string{"Blocks WebFetch via or() builtin"},
		allow: "Allows Read (tool_name outside the or() list)",
	},
}

var ruleIDRe = regexp.MustCompile(`rule_id:\s*"([^"]+)"`)

func shippedRuleIDs(t *testing.T) map[string]bool {
	t.Helper()
	dir := policiesDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read policies dir: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range ruleIDRe.FindAllStringSubmatch(string(src), -1) {
			ids[m[1]] = true
		}
	}
	return ids
}

func policiesMd(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(filepath.Dir(policiesDir(t)), "policies.md"))
	if err != nil {
		t.Fatalf("read policies.md: %v", err)
	}
	return string(src)
}

func hasBlock(md, title string) bool {
	return strings.Contains(md, "### "+title+"\n")
}

func TestPolicyCoverage_DenyAndNearMissAllow(t *testing.T) {
	md := policiesMd(t)
	ids := shippedRuleIDs(t)

	for id, cov := range policyCoverage {
		if !ids[id] {
			t.Errorf("manifest entry %q has no shipped policy under tests/policies/", id)
		}
		if len(cov.deny) == 0 {
			t.Errorf("%s: manifest lists no deny block", id)
		}
		for _, title := range cov.deny {
			if !hasBlock(md, title) {
				t.Errorf("%s: deny block %q missing from policies.md", id, title)
			}
		}
		if cov.allow == "" {
			t.Errorf("%s: manifest lists no near-miss allow block", id)
		} else if !hasBlock(md, cov.allow) {
			t.Errorf("%s: near-miss allow block %q missing from policies.md", id, cov.allow)
		}
	}

	var uncovered []string
	for id := range ids {
		if _, ok := policyCoverage[id]; !ok {
			uncovered = append(uncovered, id)
		}
	}
	sort.Strings(uncovered)
	for _, id := range uncovered {
		t.Errorf("shipped policy %q has no coverage manifest entry (needs ≥1 deny and a near-miss allow)", id)
	}
}
