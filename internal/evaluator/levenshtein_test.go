package evaluator

import (
	"strings"
	"testing"
)

// Identical strings have distance 0.
func TestLevenshtein_Identity(t *testing.T) {
	cases := []string{"", "a", "hello", "kitten", "café"}
	for _, s := range cases {
		if got := levenshtein(s, s); got != 0 {
			t.Errorf("levenshtein(%q, %q) = %d, want 0", s, s, got)
		}
	}
}

// One elementary edit (insert, delete, substitute) has distance 1.
func TestLevenshtein_SingleEdit(t *testing.T) {
	cases := []struct {
		name, a, b string
		want       int
	}{
		{"insertion", "cat", "cats", 1},
		{"deletion", "cats", "cat", 1},
		{"substitution", "cat", "bat", 1},
		{"leading insertion", "at", "cat", 1},
		{"trailing deletion", "cats", "cat", 1},
		{"middle substitution", "cat", "cut", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := levenshtein(tc.a, tc.b); got != tc.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// Canonical textbook fixture: kitten→sitting is 3 edits.
func TestLevenshtein_CanonicalKittenSitting(t *testing.T) {
	if got := levenshtein("kitten", "sitting"); got != 3 {
		t.Errorf("levenshtein(\"kitten\", \"sitting\") = %d, want 3", got)
	}
}

// Distance from "" to any string equals its rune length.
func TestLevenshtein_Empty(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "abc", 3},
		{"abc", "", 3},
		{"", "", 0},
		{"", "a", 1},
		{"hello", "", 5},
	}
	for _, tc := range cases {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// Distance is measured over runes, not UTF-8 bytes.
func TestLevenshtein_UnicodeRunes(t *testing.T) {
	cases := []struct {
		name, a, b string
		want       int
	}{
		{"café vs cafe (accent drop)", "café", "cafe", 1},
		{"café vs café (identity)", "café", "café", 0},
		{"naïve vs naive", "naïve", "naive", 1},
		{"日本 vs 日 (delete one rune)", "日本", "日", 1},
		{"α vs β (one-rune substitution)", "α", "β", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := levenshtein(tc.a, tc.b); got != tc.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d (distance is over runes, not bytes)",
					tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// levenshtein(a, b) == levenshtein(b, a) for all fixtures.
func TestLevenshtein_Symmetry(t *testing.T) {
	// ≥10 fixture pairs, covering: identity, single edits, length disparity,
	// multi-edit, empty, unicode, shared prefix/suffix.
	pairs := []struct{ a, b string }{
		{"", ""},
		{"a", "b"},
		{"cat", "bat"},
		{"kitten", "sitting"},
		{"flags", "flag"},
		{"flags", "forced"},
		{"command", "file_path"},
		{"force", ""},
		{"café", "cafe"},
		{"日本", "日"},
		{"abcdef", "abcfed"},
		{"hello world", "world hello"},
	}
	if len(pairs) < 10 {
		t.Fatalf("symmetry fixture table must have ≥10 entries; got %d", len(pairs))
	}
	for _, p := range pairs {
		ab := levenshtein(p.a, p.b)
		ba := levenshtein(p.b, p.a)
		if ab != ba {
			t.Errorf("symmetry violated: levenshtein(%q, %q)=%d vs (%q, %q)=%d",
				p.a, p.b, ab, p.b, p.a, ba)
		}
	}
}

// Broad reference table across unit edits, multi-edit, affixes, and unicode.
func TestLevenshtein_Reference(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// identity
		{"", "", 0},
		{"a", "a", 0},
		{"abc", "abc", 0},
		// single edits
		{"a", "b", 1},
		{"a", "", 1},
		{"", "a", 1},
		{"ab", "a", 1},
		{"a", "ab", 1},
		{"ab", "ba", 2}, // two substitutions OR one transposition; classic Levenshtein = 2
		// canonical
		{"kitten", "sitting", 3},
		// longer examples
		{"intention", "execution", 5},
		{"Saturday", "Sunday", 3},
		{"book", "back", 2},
		{"distance", "difference", 5},
		// did-you-mean fixtures from F5
		{"flags", "flag", 1},
		{"flags", "forced", 5},
		{"command", "file_path", 8},
		// unicode
		{"café", "cafe", 1},
		{"naïve", "naive", 1},
		{"日本語", "日本", 1},
		{"αβγ", "αβ", 1},
		// shared affixes
		{"prefix_xy", "prefix_ab", 2},
		{"_common", "_commun", 1},
	}
	if len(cases) < 20 {
		t.Fatalf("reference fixture table must have ≥20 entries; got %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			if got := levenshtein(tc.a, tc.b); got != tc.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// ~50-rune inputs terminate with distance in [0, maxLen].
func TestLevenshtein_LargeInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-input termination check under -short")
	}
	a := strings.Repeat("abcdefghij", 5) // 50 runes
	b := strings.Repeat("jihgfedcba", 5) // 50 runes, reversed alphabet groups
	got := levenshtein(a, b)
	if got < 0 || got > 50 {
		t.Errorf("levenshtein over 50-rune inputs returned %d; expected in [0, 50]", got)
	}
	// Also confirm symmetry on the large pair.
	if rev := levenshtein(b, a); rev != got {
		t.Errorf("large-input symmetry violated: %d vs %d", got, rev)
	}
}
