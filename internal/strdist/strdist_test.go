package strdist_test

import (
	"reflect"
	"testing"

	"github.com/srnnkls/fas/internal/strdist"
)

func TestDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "a", 0},
		{"abc", "abc", 0},
		{"a", "b", 1},
		{"a", "", 1},
		{"", "abc", 3},
		{"abc", "", 3},
		{"ab", "a", 1},
		{"ab", "ba", 2},
		{"#Explor", "#Explore", 1},
		{"#PreToolUze", "#PreToolUse", 1},
		{"kitten", "sitting", 3},
		{"intention", "execution", 5},
		{"Saturday", "Sunday", 3},
		{"book", "back", 2},
		{"flags", "flag", 1},
		{"flags", "forced", 5},
		{"command", "file_path", 8},
		{"café", "cafe", 1},
		{"naïve", "naive", 1},
		{"日本語", "日本", 1},
		{"αβγ", "αβ", 1},
		{"prefix_xy", "prefix_ab", 2},
	}
	for _, c := range cases {
		if got := strdist.Distance(c.a, c.b); got != c.want {
			t.Errorf("Distance(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
		if got := strdist.Distance(c.b, c.a); got != c.want {
			t.Errorf("Distance(%q,%q)=%d want %d (symmetry)", c.b, c.a, got, c.want)
		}
	}
}

func TestNearest(t *testing.T) {
	cands := []string{"#Explore", "#Plan", "#GeneralPurpose"}

	t.Run("within threshold returns closest", func(t *testing.T) {
		got := strdist.Nearest("#Explor", cands, 3, 2)
		if !reflect.DeepEqual(got, []string{"#Explore"}) {
			t.Fatalf("got %v want [#Explore]", got)
		}
	})

	t.Run("beyond threshold returns none", func(t *testing.T) {
		got := strdist.Nearest("#Zzzzz", cands, 3, 2)
		if len(got) != 0 {
			t.Fatalf("got %v want empty", got)
		}
	})

	t.Run("top-N cap", func(t *testing.T) {
		got := strdist.Nearest("aa", []string{"ab", "ac", "ad", "ae"}, 2, 2)
		if len(got) != 2 {
			t.Fatalf("got %v want 2 results", got)
		}
	})

	t.Run("tie-break ascending string at equal distance", func(t *testing.T) {
		got := strdist.Nearest("aa", []string{"ad", "ab", "ac"}, 3, 1)
		want := []string{"ab", "ac", "ad"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("ascending distance ordering", func(t *testing.T) {
		got := strdist.Nearest("abc", []string{"abXc", "abc"}, 3, 2)
		want := []string{"abc", "abXc"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("empty candidates", func(t *testing.T) {
		if got := strdist.Nearest("x", nil, 3, 2); len(got) != 0 {
			t.Fatalf("got %v want empty", got)
		}
	})
}
