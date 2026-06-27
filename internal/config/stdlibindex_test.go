package config

import (
	"io/fs"
	"path"
	"reflect"
	"slices"
	"testing"

	fascue "github.com/srnnkls/fas/cue"
)

func TestStdlibIndex_DiscoversMembers(t *testing.T) {
	idx := StdlibIndex()

	cases := []struct {
		pkg, member string
	}{
		{"hook", "#PreToolUse"},
		{"hook", "#SubagentStart"},
		{"agent", "#Explore"},
		{"tool", "#Bash"},
	}
	for _, c := range cases {
		if !slices.Contains(idx[c.pkg], c.member) {
			t.Errorf("StdlibIndex()[%q] = %v, missing %q", c.pkg, idx[c.pkg], c.member)
		}
	}

	if slices.Contains(idx["agent"], "#Explor") {
		t.Errorf("index should not contain the typo #Explor")
	}
	if _, ok := idx["nosuchpackage"]; ok {
		t.Errorf("index should not contain an unknown package")
	}
}

func TestStdlibIndex_KeysMatchEmbeddedPackages(t *testing.T) {
	want := map[string]struct{}{}
	stdlib := fascue.StdlibFS()
	err := fs.WalkDir(stdlib, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || path.Ext(p) != ".cue" {
			return nil
		}
		if dir := path.Dir(p); dir != "." {
			want[dir] = struct{}{}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk stdlib: %v", err)
	}

	idx := StdlibIndex()
	got := map[string]struct{}{}
	for k := range idx {
		got[k] = struct{}{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("index keys = %v, want sub-package dirs %v", keysOf(got), keysOf(want))
	}
}

func TestStdlibIndex_Memoized(t *testing.T) {
	a := StdlibIndex()
	b := StdlibIndex()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("memoized index differs between calls")
	}
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
