package cue_test

import (
	"testing"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// STS-004 extends this file with the live fuzz target and Go reference predicate.

// recursiveFlagMatcher loads flag.#hasRmRecursive once and returns a closure
// that pays only per-call CompileString + Unify + Validate. Each token is
// lifted into a single-element flags list, the shape list.MatchN runs over.
func recursiveFlagMatcher(t *testing.T) func(token string) bool {
	t.Helper()
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)
	cons := lookupDef(t, pkg, "hasRmRecursive")

	return func(token string) bool {
		val := ctx.CompileString(flagsInput(token), cue.Filename("token.cue"))
		if val.Err() != nil {
			return false
		}
		return cons.Unify(val).Validate(cue.Concrete(false)) == nil
	}
}

var recursiveSpikeTokens = []string{
	"-r", "-R", "-rf", "-fR", "--recursive", "--recursive=x", "-vrf", "-f", "--force", "-x",
}

func TestSpike_RecursiveMatcherThroughput(t *testing.T) {
	match := recursiveFlagMatcher(t)

	if !match("-rf") {
		t.Fatal("closure failed to match -rf")
	}
	if match("-f") {
		t.Fatal("closure matched -f")
	}

	const iters = 2000
	start := time.Now()
	for i := range iters {
		match(recursiveSpikeTokens[i%len(recursiveSpikeTokens)])
	}
	elapsed := time.Since(start)

	t.Logf("recursive matcher: %d iters in %s — %.0f matches/sec, %s/call",
		iters, elapsed, float64(iters)/elapsed.Seconds(), elapsed/iters)
}
