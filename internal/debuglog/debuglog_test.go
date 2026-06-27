package debuglog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpen_Disabled(t *testing.T) {
	var warn strings.Builder
	r := Open("", "", nil, &warn)
	if r != nil {
		t.Fatal("expected nil recorder when FAS_LOG is empty")
	}
	if warn.Len() != 0 {
		t.Errorf("expected no warning when FAS_LOG unset, got %q", warn.String())
	}
	// Nil recorder methods must not panic.
	r.SetRawInput([]byte(`{}`))
	r.SetInput(nil)
	r.SetRules(nil, nil)
	r.SetMatches(nil)
	r.SetOutput(nil)
	r.Close(0)
}

func TestOpen_TruthyValues(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "YES"} {
		r := Open(v, "", []string{"eval"}, nil)
		if r == nil {
			t.Errorf("FAS_LOG=%q: expected non-nil recorder", v)
			continue
		}
		if r.dir == "" {
			t.Errorf("FAS_LOG=%q: expected non-empty dir", v)
		}
	}
}

func TestOpen_CustomDir(t *testing.T) {
	dir := t.TempDir()
	r := Open(dir, "", []string{"eval"}, nil)
	if r == nil {
		t.Fatal("expected non-nil recorder")
	}
	if r.dir != dir {
		t.Errorf("dir=%q, want %q", r.dir, dir)
	}
}

func TestOpen_WarnsOnUnusableDir(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(notADir, "logs")

	var warn strings.Builder
	r := Open(dir, "", []string{"eval"}, &warn)
	if r != nil {
		t.Fatal("expected nil recorder when dir cannot be created")
	}
	if !strings.Contains(warn.String(), "FAS_LOG") {
		t.Errorf("expected FAS_LOG warning, got %q", warn.String())
	}
}

func TestRecorder_WritesFile(t *testing.T) {
	dir := t.TempDir()
	r := Open(dir, "", []string{"eval", "--harness", "claude"}, nil)
	r.SetRawInput([]byte(`{"hook_event_name":"PreToolUse"}`))
	r.SetInput(map[string]string{"hook_event_name": "PreToolUse"})
	r.SetRules([]string{"r1"}, []string{"r2"})
	r.SetMatches([]MatchSummary{{RuleID: "r1", Kind: "deny", Source: "test.cue:rule"}})
	r.SetOutput([]byte(`{"result":"ok"}`))
	r.Close(0)

	files, err := logFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}

	data, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry.ExitCode != 0 {
		t.Errorf("exit_code=%d, want 0", entry.ExitCode)
	}
	if len(entry.Args) != 3 {
		t.Errorf("args len=%d, want 3", len(entry.Args))
	}
	if entry.Rules == nil || len(entry.Rules.Global) != 1 {
		t.Error("expected 1 global rule")
	}
	if len(entry.Matches) != 1 {
		t.Errorf("matches len=%d, want 1", len(entry.Matches))
	}
}

func TestGC_RemovesOldFiles(t *testing.T) {
	dir := t.TempDir()

	old := filepath.Join(dir, "20200101T000000Z-aaaaaaaa.json")
	if err := os.WriteFile(old, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	fresh := filepath.Join(dir, "20250101T000000Z-bbbbbbbb.json")
	if err := os.WriteFile(fresh, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	gc(dir, time.Hour)

	files, err := logFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1 (old should be removed)", len(files))
	}
	if files[0].Name() != "20250101T000000Z-bbbbbbbb.json" {
		t.Errorf("remaining file=%q, want the fresh one", files[0].Name())
	}
}

func TestGC_SkipsNonJSON(t *testing.T) {
	dir := t.TempDir()

	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(other, past, past); err != nil {
		t.Fatal(err)
	}

	gc(dir, time.Hour)

	if _, err := os.Stat(other); err != nil {
		t.Error("non-JSON file should not be removed by GC")
	}
}

func TestGC_PreservesForeignJSON(t *testing.T) {
	dir := t.TempDir()

	foreign := filepath.Join(dir, "package.json")
	if err := os.WriteFile(foreign, []byte(`{"name":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(foreign, past, past); err != nil {
		t.Fatal(err)
	}

	gc(dir, time.Hour)

	if _, err := os.Stat(foreign); err != nil {
		t.Error("foreign .json not matching the log-file pattern must survive GC")
	}
}

func TestParseTTL(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"", defaultTTL},
		{"30m", 30 * time.Minute},
		{"24h", 24 * time.Hour},
		{"invalid", defaultTTL},
		{"-1h", defaultTTL},
	}
	for _, tt := range tests {
		got := parseTTL(tt.raw)
		if got != tt.want {
			t.Errorf("parseTTL(%q)=%v, want %v", tt.raw, got, tt.want)
		}
	}
}
