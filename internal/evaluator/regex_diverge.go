package evaluator

import (
	"regexp"
	"regexp/syntax"
)

// regexDiverge computes the byte offset at which the longest matching anchored
// prefix of pattern stopped matching input. Returns -1 when no useful
// divergence can be computed (parse error, empty/non-concat root, unanchored
// pattern, complex sub-expressions) and when the full pattern matches (no
// failure to localise).
//
// Strategy (per scope.md gotchas):
//  1. Parse pattern with regexp/syntax.Parse. Parse errors bail to -1.
//  2. Require OpConcat at the root and OpBeginText as the first atom. Other
//     top-level shapes (alternation, bare literal, unanchored) bail to -1.
//  3. Expand OpLiteral runs into per-rune atoms so divergence can land between
//     characters of a literal run.
//  4. Reject capture groups and nested alternations defensively — they would
//     confuse the atom-wise prefix strategy.
//  5. Binary-search over prefix lengths. For each candidate i, synthesize
//     concat(atoms[0..i]) via syntax.Regexp.String(), compile with Go's
//     stdlib regexp, run FindStringIndex on input. The largest i with a
//     non-nil match wins.
//  6. If that i equals len(atoms), the full pattern matched → return -1.
//     Otherwise return the end byte offset of the last successful prefix.
//
// The returned offset is a byte offset (not a rune offset); multi-byte UTF-8
// input preserves that convention.
func regexDiverge(pattern, input string) int {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return -1
	}
	if re.Op != syntax.OpConcat || len(re.Sub) == 0 {
		return -1
	}
	if re.Sub[0].Op != syntax.OpBeginText {
		return -1
	}
	atoms, ok := enumerateAtoms(re.Sub)
	if !ok || len(atoms) == 0 {
		return -1
	}

	// Binary search for the largest k in [0, len(atoms)] such that the
	// prefix concat(atoms[0..k]) matches. Invariant: prefixMatch(0) is
	// trivially true (empty concat matches), so lo starts at 0 and we
	// track the end offset of the best match in bestEnd.
	lo, hi := 0, len(atoms)
	bestK, bestEnd := 0, 0
	for lo <= hi {
		mid := (lo + hi) / 2
		end, matched := prefixMatch(atoms, mid, input)
		if matched {
			if mid >= bestK {
				bestK = mid
				bestEnd = end
			}
			lo = mid + 1
			continue
		}
		hi = mid - 1
	}

	// Full pattern matched → no useful divergence sentinel.
	if bestK == len(atoms) {
		return -1
	}
	return bestEnd
}

// enumerateAtoms flattens the root concatenation into divergence atoms.
// Literal runs split into per-rune atoms so prefix divergence can land between
// characters of a literal. Complex forms (capture groups, nested alternation)
// bail the whole computation via the false return.
func enumerateAtoms(subs []*syntax.Regexp) ([]*syntax.Regexp, bool) {
	out := make([]*syntax.Regexp, 0, len(subs))
	for _, s := range subs {
		switch s.Op {
		case syntax.OpAlternate, syntax.OpCapture:
			return nil, false
		case syntax.OpLiteral:
			if len(s.Rune) <= 1 {
				out = append(out, s)
				continue
			}
			for _, r := range s.Rune {
				out = append(out, &syntax.Regexp{
					Op:    syntax.OpLiteral,
					Flags: s.Flags,
					Rune:  []rune{r},
				})
			}
		default:
			out = append(out, s)
		}
	}
	return out, true
}

// prefixMatch compiles the first k atoms as a single concat pattern and runs
// FindStringIndex on input. Returns the end byte offset of the match plus true
// when a match is found; returns (0, false) on compile error or no match. The
// empty-prefix case (k == 0) is handled inline as a zero-length match at 0.
func prefixMatch(atoms []*syntax.Regexp, k int, input string) (int, bool) {
	if k == 0 {
		return 0, true
	}
	pref := &syntax.Regexp{Op: syntax.OpConcat, Sub: atoms[:k]}
	c, err := regexp.Compile(pref.String())
	if err != nil {
		return 0, false
	}
	loc := c.FindStringIndex(input)
	if loc == nil {
		return 0, false
	}
	return loc[1], true
}
