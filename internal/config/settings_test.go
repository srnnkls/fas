package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/srnnkls/fas/internal/config"
)

func writeSettings(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.cue")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	yes, no := true, false

	settings, err := config.LoadSettings(filepath.Join(t.TempDir(), "missing.cue"))
	if err != nil || settings != (config.Settings{}) {
		t.Fatalf("missing: %+v %v", settings, err)
	}

	settings, err = config.LoadSettings(writeSettings(t, `package fas

project_rules:   ".rules"
global_rules:    "~/rules"
follow_symlinks: true
fail_closed:     false
explain:         "both"
format:          "json"
color:           "never"
log:             "~/logs"
log_ttl:         "1h30m"
`))
	want := config.Settings{
		ProjectRules: ".rules", GlobalRules: filepath.Join(home, "rules"),
		FollowSymlinks: &yes, FailClosed: &no, Explain: "both", Format: "json",
		Color: "never", Log: filepath.Join(home, "logs"), LogTTL: "1h30m",
	}
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, settings); diff != "" {
		t.Fatal(diff)
	}

	for src, want := range map[string]string{"log: true\n": "1", "log: false\n": "", "// empty\n": ""} {
		settings, err := config.LoadSettings(writeSettings(t, src))
		if err != nil || settings.Log != want {
			t.Fatalf("%q: log=%q %v", src, settings.Log, err)
		}
	}

	for _, src := range []string{
		"follow_symlink: true\n",
		"follow_symlinks: \"yes\"\n",
		"follow_symlinks: bool\n",
		"explain: \"all\"\n",
		"format: \"yaml\"\n",
		"color: \"sometimes\"\n",
		"global_rules: \"\"\n",
		"log: 1\n",
		"log_ttl: \"forever\"\n",
		"harness: \"pi\"\n",
	} {
		path := writeSettings(t, src)
		if _, err := config.LoadSettings(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%q: accepted or unattributed: %v", src, err)
		}
	}
}
