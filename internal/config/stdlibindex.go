package config

import (
	"io/fs"
	"path"
	"path/filepath"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"

	fascue "github.com/srnnkls/fas/cue"
)

var (
	stdlibIndexOnce  sync.Once
	stdlibIndexValue map[string][]string
)

// StdlibIndex returns each embedded stdlib sub-package mapped to its exported
// definition names (e.g. "agent" -> ["#Explore", ...]). Packages and members
// are discovered from the embedded FS, so the index tracks whatever stdlib the
// binary ships. The result is memoized and must not be mutated by callers.
func StdlibIndex() map[string][]string {
	stdlibIndexOnce.Do(func() {
		stdlibIndexValue = buildStdlibIndex()
	})
	return stdlibIndexValue
}

func buildStdlibIndex() map[string][]string {
	pkgs := discoverStdlibPackages()
	if len(pkgs) == 0 {
		return map[string][]string{}
	}
	overlay, err := buildStdlibOverlay()
	if err != nil {
		return map[string][]string{}
	}
	overlay[filepath.Join(RulesModuleRoot, "cue.mod", "module.cue")] = load.FromString(
		"module: \"" + rulesModulePath + "\"\nlanguage: version: \"v0.11.0\"\n",
	)

	ctx := cuecontext.New()
	out := make(map[string][]string, len(pkgs))
	for _, pkg := range pkgs {
		members := stdlibPackageMembers(ctx, overlay, pkg)
		if members != nil {
			out[pkg] = members
		}
	}
	return out
}

func discoverStdlibPackages() []string {
	seen := map[string]struct{}{}
	stdlib := fascue.StdlibFS()
	_ = fs.WalkDir(stdlib, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || path.Ext(p) != ".cue" {
			return nil
		}
		if dir := path.Dir(p); dir != "." {
			seen[dir] = struct{}{}
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for dir := range seen {
		out = append(out, dir)
	}
	return out
}

func stdlibPackageMembers(ctx *cue.Context, overlay map[string]load.Source, pkg string) []string {
	importPath := fascue.StdlibImportPathPrefix + "/" + pkg
	cfg := &load.Config{Dir: RulesModuleRoot, ModuleRoot: RulesModuleRoot, Overlay: overlay}
	insts := load.Instances([]string{importPath}, cfg)
	if len(insts) == 0 || insts[0].Err != nil {
		return nil
	}
	val := ctx.BuildInstance(insts[0])
	if val.Err() != nil {
		return nil
	}
	iter, err := val.Fields(cue.Definitions(true))
	if err != nil {
		return nil
	}
	var members []string
	for iter.Next() {
		members = append(members, iter.Selector().String())
	}
	return members
}
