package config

import (
	"slices"
	"strings"

	"github.com/srnnkls/fas/internal/strdist"
)

type candidate struct {
	key     string
	display string
}

func suggest(refPkg, missing string, local []string, idx map[string][]string) string {
	maxDist := max(2, len([]rune(missing))/3)
	const topN = 3

	var ranked []string
	if refPkg != "" {
		ranked = rankCandidates(missing, qualifiedCandidates(refPkg, idx[refPkg]), maxDist, topN)
		if len(ranked) == 0 {
			ranked = rankCandidates(missing, crossPackageCandidates(refPkg, idx), maxDist, topN)
		}
	} else {
		cands := make([]candidate, 0, len(local))
		for _, name := range local {
			cands = append(cands, candidate{key: name, display: name})
		}
		cands = append(cands, allStdlibCandidates(idx)...)
		ranked = rankCandidates(missing, cands, maxDist, topN)
	}
	return phrase(ranked)
}

func qualifiedCandidates(pkg string, members []string) []candidate {
	out := make([]candidate, len(members))
	for i, m := range members {
		out[i] = candidate{key: m, display: pkg + "." + m}
	}
	return out
}

func crossPackageCandidates(exclude string, idx map[string][]string) []candidate {
	out := make([]candidate, 0, countMembers(idx))
	for _, pkg := range sortedKeys(idx) {
		if pkg == exclude {
			continue
		}
		out = append(out, qualifiedCandidates(pkg, idx[pkg])...)
	}
	return out
}

func allStdlibCandidates(idx map[string][]string) []candidate {
	out := make([]candidate, 0, countMembers(idx))
	for _, pkg := range sortedKeys(idx) {
		out = append(out, qualifiedCandidates(pkg, idx[pkg])...)
	}
	return out
}

func countMembers(idx map[string][]string) int {
	n := 0
	for _, members := range idx {
		n += len(members)
	}
	return n
}

func rankCandidates(missing string, cands []candidate, maxDist, n int) []string {
	type scored struct {
		display string
		dist    int
	}
	var hits []scored
	for _, c := range cands {
		if d := strdist.Distance(missing, c.key); d <= maxDist {
			hits = append(hits, scored{display: c.display, dist: d})
		}
	}
	slices.SortFunc(hits, func(a, b scored) int {
		if a.dist != b.dist {
			return a.dist - b.dist
		}
		return strings.Compare(a.display, b.display)
	})
	if len(hits) > n {
		hits = hits[:n]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.display
	}
	return out
}

func phrase(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "did you mean `" + names[0] + "`?"
	default:
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = "`" + n + "`"
		}
		return "did you mean one of: " + strings.Join(quoted, ", ") + "?"
	}
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
