package config_test

import (
	"testing"

	"github.com/srnnkls/fas/internal/config"
)

// CRP-004 — total rule order. The exported comparator config.CompareModulePath
// defines the DIRECTORY dimension of the order: it compares two
// module-relative rule paths by their DIRECTORY part lexically FIRST, then by
// BASENAME lexically. filepath.Dir("x.cue") == "." is the flat/root dir, which
// sorts before any named subdir. Declaration index (handled by LoadRules) only
// breaks ties WITHIN a single file.
//
// RED today: config.CompareModulePath does not exist, so this file fails to
// compile. The compile failure IS the red signal; the table additionally pins
// the (dir-lexical, then basename) contract so a naive full-path string-sort
// implementation also fails.

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

func TestCompareModulePath_TotalOrder(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int // expected sign of CompareModulePath(a, b)
	}{
		{
			// Key case proving it is NOT a naive full-path string sort: as raw
			// strings "a/x.cue" < "ab.cue", but the FLAT file's dir "." sorts
			// before dir "a", so the flat file must come first.
			name: "flat before subdir despite raw-string order",
			a:    "ab.cue",
			b:    "a/x.cue",
			want: -1,
		},
		{
			name: "same dir, filename lexical",
			a:    "security/auth.cue",
			b:    "security/git.cue",
			want: -1,
		},
		{
			name: "different dirs lexical",
			a:    "security/z.cue",
			b:    "workflow/a.cue",
			want: -1,
		},
		{
			name: "flat file before any subdir",
			a:    "z.cue",
			b:    "a/a.cue",
			want: -1,
		},
		{
			name: "equality",
			a:    "x/y.cue",
			b:    "x/y.cue",
			want: 0,
		},
		{
			// dir("a/b/c.cue") == "a/b", dir("a/b2.cue") == "a"; "a/b" > "a"
			// lexically (it extends "a" with "/b"), so the deeper file sorts
			// AFTER the shallower sibling. Pins (dir-lexical) over basename.
			name: "deep nesting compares full dir path",
			a:    "a/b/c.cue",
			b:    "a/b2.cue",
			want: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sign(config.CompareModulePath(tc.a, tc.b))
			if got != tc.want {
				t.Errorf("sign(CompareModulePath(%q, %q)) = %d, want %d",
					tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestCompareModulePath_Antisymmetry(t *testing.T) {
	pairs := [][2]string{
		{"ab.cue", "a/x.cue"},
		{"security/auth.cue", "security/git.cue"},
		{"security/z.cue", "workflow/a.cue"},
		{"z.cue", "a/a.cue"},
		{"a/b/c.cue", "a/b2.cue"},
		{"x/y.cue", "x/y.cue"},
	}
	for _, p := range pairs {
		a, b := p[0], p[1]
		ab := sign(config.CompareModulePath(a, b))
		ba := sign(config.CompareModulePath(b, a))
		if ab != -ba {
			t.Errorf("antisymmetry violated for (%q,%q): sign(a,b)=%d sign(b,a)=%d",
				a, b, ab, ba)
		}
	}
}
