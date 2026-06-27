// Package debuglog records full request/response payloads for every fas
// invocation when the FAS_LOG environment variable is set. Each invocation
// writes a single JSON file to the log directory; files older than FAS_LOG_TTL
// (default 1h) are garbage-collected at the start of each run.
//
// Enable:
//
//	FAS_LOG=1              # uses default dir ~/.local/state/fas/logs
//	FAS_LOG=/custom/path   # uses that directory
//
// Control retention:
//
//	FAS_LOG_TTL=30m        # keep logs for 30 minutes
//	FAS_LOG_TTL=24h        # keep logs for 24 hours
package debuglog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const defaultTTL = time.Hour

var logFileRE = regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{8}\.json$`)

// Entry is the full debug record for one fas invocation.
type Entry struct {
	Timestamp string          `json:"timestamp"`
	Args      []string        `json:"args"`
	RawInput  json.RawMessage `json:"raw_input,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Rules     *RuleSummary    `json:"rules,omitempty"`
	Matches   []MatchSummary  `json:"matches,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	ExitCode  int             `json:"exit_code"`
}

// RuleSummary lists the rule IDs loaded from each layer.
type RuleSummary struct {
	Global  []string `json:"global"`
	Project []string `json:"project"`
}

// MatchSummary records a single rule match.
type MatchSummary struct {
	RuleID string `json:"rule_id"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

// Recorder accumulates debug data throughout a fas invocation and writes it
// to disk at the end. A nil *Recorder is safe to call — all methods are
// no-ops, so callers can use it unconditionally without nil checks.
type Recorder struct {
	dir   string
	entry Entry
}

// Open resolves the FAS_LOG and FAS_LOG_TTL environment variables and returns
// a Recorder. An empty FAS_LOG returns nil silently. A non-empty FAS_LOG whose
// directory cannot be resolved or created writes one line to warn and returns
// nil. The log directory is created if absent. GC runs before returning.
func Open(fasLog, fasLogTTL string, args []string, warn io.Writer) *Recorder {
	if fasLog == "" {
		return nil
	}

	dir := resolveDir(fasLog)
	if dir == "" {
		warnf(warn, "fas: FAS_LOG set but home directory lookup failed; logging disabled\n")
		return nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		warnf(warn, "fas: FAS_LOG set but cannot create %s: %v; logging disabled\n", dir, err)
		return nil
	}

	ttl := parseTTL(fasLogTTL)
	gc(dir, ttl)

	return &Recorder{
		dir: dir,
		entry: Entry{
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Args:      args,
		},
	}
}

// SetRawInput records the raw bytes read from stdin.
func (r *Recorder) SetRawInput(raw []byte) {
	if r == nil {
		return
	}
	if json.Valid(raw) {
		r.entry.RawInput = json.RawMessage(raw)
	} else {
		quoted, _ := json.Marshal(string(raw))
		r.entry.RawInput = json.RawMessage(quoted)
	}
}

// SetInput records the preprocessed input envelope.
func (r *Recorder) SetInput(v any) {
	if r == nil {
		return
	}
	if data, err := json.Marshal(v); err == nil {
		r.entry.Input = json.RawMessage(data)
	}
}

// SetRules records the loaded rule IDs.
func (r *Recorder) SetRules(global, project []string) {
	if r == nil {
		return
	}
	if global == nil {
		global = []string{}
	}
	if project == nil {
		project = []string{}
	}
	r.entry.Rules = &RuleSummary{Global: global, Project: project}
}

// SetMatches records the evaluation matches.
func (r *Recorder) SetMatches(matches []MatchSummary) {
	if r == nil {
		return
	}
	r.entry.Matches = matches
}

// SetOutput records the rendered response bytes.
func (r *Recorder) SetOutput(raw []byte) {
	if r == nil {
		return
	}
	if json.Valid(raw) {
		r.entry.Output = json.RawMessage(raw)
	} else {
		quoted, _ := json.Marshal(string(raw))
		r.entry.Output = json.RawMessage(quoted)
	}
}

// Close writes the accumulated entry to disk. It is safe to call on a nil
// Recorder. The exit code is set at close time because it may depend on
// events after the last recording call.
func (r *Recorder) Close(exitCode int) {
	if r == nil {
		return
	}
	r.entry.ExitCode = exitCode
	data, err := json.MarshalIndent(r.entry, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')

	name := fmt.Sprintf("%s-%s.json",
		time.Now().UTC().Format("20060102T150405Z"),
		shortID())
	_ = os.WriteFile(filepath.Join(r.dir, name), data, 0o600)
}

func warnf(w io.Writer, format string, a ...any) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, format, a...)
}

func resolveDir(fasLog string) string {
	if fasLog == "" {
		return ""
	}
	switch strings.ToLower(fasLog) {
	case "1", "true", "yes":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".local", "state", "fas", "logs")
	default:
		return fasLog
	}
}

func parseTTL(raw string) time.Duration {
	if raw == "" {
		return defaultTTL
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultTTL
	}
	return d
}

func gc(dir string, ttl time.Duration) {
	cutoff := time.Now().Add(-ttl)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !logFileRE.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func shortID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}

func logFiles(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []fs.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, e)
		}
	}
	return out, nil
}
