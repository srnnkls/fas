// Package strdist provides rune-scoped edit-distance ranking for suggestions.
package strdist

import "slices"

// Distance returns the rune-scoped Levenshtein edit distance between a and b.
func Distance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	n, m := len(ar), len(br)
	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}
	prev := make([]int, m+1)
	curr := make([]int, m+1)
	for j := 0; j <= m; j++ {
		prev[j] = j
	}
	for i := 1; i <= n; i++ {
		curr[0] = i
		for j := 1; j <= m; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[m]
}

// Nearest returns up to n candidates within maxDist of name, closest first,
// ties broken by ascending candidate string.
func Nearest(name string, candidates []string, n, maxDist int) []string {
	type scored struct {
		cand string
		dist int
	}
	var hits []scored
	for _, c := range candidates {
		if d := Distance(name, c); d <= maxDist {
			hits = append(hits, scored{cand: c, dist: d})
		}
	}
	slices.SortFunc(hits, func(a, b scored) int {
		if a.dist != b.dist {
			return a.dist - b.dist
		}
		return slices.Compare([]rune(a.cand), []rune(b.cand))
	})
	if len(hits) > n {
		hits = hits[:n]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.cand
	}
	return out
}
