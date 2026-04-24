package evaluator

import "testing"

// -----------------------------------------------------------------------------
// T6 — regex divergence helper
//
// These white-box tests pin the contract of the unexported `regexDiverge`
// helper: given a CUE regex pattern and an input string, return the byte
// offset at which the longest-matching anchored prefix ended, or -1 when no
// useful divergence can be computed (complex patterns, parse errors,
// full-match, empty pattern).
//
// Strategy per scope.md gotchas:
//   1. Parse pattern with regexp/syntax.Parse(p, syntax.Perl) → AST.
//   2. If AST root is a concatenation, enumerate its atoms.
//   3. For each prefix Pᵢ, compile with Go stdlib regexp and run
//      FindStringIndex against input. Largest i with a match wins; the
//      match's end byte offset becomes DivergeAt.
//   4. Complex patterns (top-level alternation, lookarounds, non-concat
//      roots, parse errors) bail to -1.
//
// Contract pins (see task doc):
//   - Full match → DivergeAt = -1 (regex didn't fail — safety value).
//   - Parse error → DivergeAt = -1, no panic.
//   - Top-level alternation → DivergeAt = -1 (spec bail).
//   - Lookahead/unsupported → DivergeAt = -1 (no panic).
//   - Empty pattern → DivergeAt = -1 (always matches; no useful offset).
//   - Offset is byte offset (multibyte input preserves that convention).
// -----------------------------------------------------------------------------

// Simple prefix match with zero-length match at position 0:
// `^rm ` vs `ls` → even the anchor+'r' fails immediately. DivergeAt=0.
func TestRegexDiverge_SimplePrefixFails_AtZero(t *testing.T) {
	got := regexDiverge("^rm ", "ls")
	if got != 0 {
		t.Errorf("regexDiverge(%q, %q) = %d, want 0", "^rm ", "ls", got)
	}
}

// Partial match then divergence: `^rm ` vs `rm-rf` matches `rm` (offset 2),
// then the expected space ' ' encounters '-'. DivergeAt=2.
func TestRegexDiverge_PartialMatchThenDiverge(t *testing.T) {
	got := regexDiverge("^rm ", "rm-rf")
	if got != 2 {
		t.Errorf("regexDiverge(%q, %q) = %d, want 2 (matched 'rm', ' ' vs '-')",
			"^rm ", "rm-rf", got)
	}
}

// Char class: `^[a-z]+$` vs `abc1` matches `abc` (offset 3), then `1` is not
// in [a-z]. DivergeAt=3.
func TestRegexDiverge_CharClass_DivergeAfterThree(t *testing.T) {
	got := regexDiverge("^[a-z]+$", "abc1")
	if got != 3 {
		t.Errorf("regexDiverge(%q, %q) = %d, want 3 (matched 'abc', '1' not in [a-z])",
			"^[a-z]+$", "abc1", got)
	}
}

// Full match: `^[a-z]+$` vs `abc` matches entirely. Contract: DivergeAt = -1
// when input fully matches (regex doesn't fire as a failure case; safety
// sentinel so callers short-circuit the "no useful divergence" case
// uniformly with parse-error and complex-pattern bails).
func TestRegexDiverge_FullMatch_ReturnsMinusOne(t *testing.T) {
	got := regexDiverge("^[a-z]+$", "abc")
	if got != -1 {
		t.Errorf("regexDiverge(%q, %q) = %d, want -1 (full match → no useful divergence)",
			"^[a-z]+$", "abc", got)
	}
}

// Pattern fails to parse: `[` is an unclosed character class. The helper
// must bail to -1 without panicking.
func TestRegexDiverge_InvalidPattern_ReturnsMinusOneNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("regexDiverge panicked on invalid pattern: %v", r)
		}
	}()
	got := regexDiverge("[", "anything")
	if got != -1 {
		t.Errorf("regexDiverge(%q, %q) = %d, want -1 (parse error bail)",
			"[", "anything", got)
	}
}

// Top-level alternation: `^foo|bar$` is an OR at root — spec says bail to -1.
func TestRegexDiverge_TopLevelAlternation_ReturnsMinusOne(t *testing.T) {
	got := regexDiverge("^foo|bar$", "baz")
	if got != -1 {
		t.Errorf("regexDiverge(%q, %q) = %d, want -1 (top-level alternation bails per spec)",
			"^foo|bar$", "baz", got)
	}
}

// Lookahead: `(?=foo)` is not supported by Go's regexp engine; regexp/syntax
// rejects the construct. Must bail to -1 without panic.
func TestRegexDiverge_Lookahead_ReturnsMinusOneNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("regexDiverge panicked on lookahead pattern: %v", r)
		}
	}()
	got := regexDiverge("^(?=foo)", "bar")
	if got != -1 {
		t.Errorf("regexDiverge(%q, %q) = %d, want -1 (lookahead unsupported)",
			"^(?=foo)", "bar", got)
	}
}

// Unicode input: offset is byte offset. `^[a-z]+$` vs `abcé` matches `abc`
// then `é` (multi-byte UTF-8, not in [a-z]) — DivergeAt = 3 (byte offset
// of `é`, which is `abc`'s byte length).
func TestRegexDiverge_UnicodeInput_ReturnsByteOffset(t *testing.T) {
	got := regexDiverge("^[a-z]+$", "abcé")
	if got != 3 {
		t.Errorf("regexDiverge(%q, %q) = %d, want 3 (byte offset of 'é')",
			"^[a-z]+$", "abcé", got)
	}
}

// Empty input against an anchored pattern: `^foo` vs `""` — nothing matches.
// DivergeAt=0 (divergence at the very first position).
func TestRegexDiverge_EmptyInput_ReturnsZero(t *testing.T) {
	got := regexDiverge("^foo", "")
	if got != 0 {
		t.Errorf("regexDiverge(%q, %q) = %d, want 0", "^foo", "", got)
	}
}

// Empty pattern matches everything vacuously. Contract: DivergeAt = -1 (no
// useful divergence — the empty pattern never "fails").
func TestRegexDiverge_EmptyPattern_ReturnsMinusOne(t *testing.T) {
	got := regexDiverge("", "x")
	if got != -1 {
		t.Errorf("regexDiverge(%q, %q) = %d, want -1 (empty pattern matches vacuously)",
			"", "x", got)
	}
}
