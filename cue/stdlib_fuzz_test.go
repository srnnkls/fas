package cue_test

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// STS-004 extends this file with the live fuzz target and Go reference predicate.

// recursiveFlagMatcher loads flag.#hasOption & opt.recursive once and returns a
// closure that pays only per-call CompileString + Unify + Validate. Each token
// is lifted into a single-element flags list, the shape list.MatchN runs over.
func recursiveFlagMatcher(t *testing.T) func(token string) bool {
	t.Helper()
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgFlag)
	hasOption := lookupDef(t, pkg, "hasOption")
	cons := hasOption.Unify(pkg.LookupPath(cue.ParsePath("opt")).LookupPath(cue.ParsePath("recursive")))
	if err := cons.Err(); err != nil {
		t.Fatalf("#hasOption & opt.recursive errored: %v", err)
	}

	return func(token string) bool {
		val := ctx.CompileString(flagsInput(token), cue.Filename("token.cue"))
		if val.Err() != nil {
			return false
		}
		return cons.Unify(val).Validate(cue.Concrete(false)) == nil
	}
}

var recursiveSpikeTokens = []string{
	"-r", "-R", "--recursive", "-f", "--force", "-x", "-rf", "--recursive=x", "-recursive",
}

func TestSpike_RecursiveMatcherThroughput(t *testing.T) {
	match := recursiveFlagMatcher(t)

	if !match("-r") {
		t.Fatal("closure failed to match -r")
	}
	if match("-f") {
		t.Fatal("closure matched -f")
	}
	// -rf is a bundle: the parser debundles it before the matcher sees it, so
	// the raw bundle must NOT match exact set membership.
	if match("-rf") {
		t.Fatal("closure matched the un-debundled bundle -rf")
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

// isRecursiveRmFlag is the reference oracle for INV-3: exact set membership over
// opt.recursive's spellings, authored without reading options.cue. The matcher
// now consumes DEBUNDLED tokens, so a token is recursive iff it is exactly one
// of the recursive spellings — bundles like -rf are debundled upstream by the
// parser. Divergence from the CUE matcher is a bug in one of them.
func isRecursiveRmFlag(token string) bool {
	switch token {
	case "-r", "-R", "--recursive":
		return true
	default:
		return false
	}
}

// isFlagToken restricts the fuzz domain to what a shell actually delivers as
// one argument: valid UTF-8, no embedded newline, no NUL (execve forbids an
// embedded NUL in argv, just as a realistic token carries no newline).
func isFlagToken(token string) bool {
	return utf8.ValidString(token) &&
		!strings.ContainsRune(token, '\n') &&
		!strings.ContainsRune(token, '\x00')
}

// FuzzRecursiveFlag pins INV-3: the CUE matcher and the man-rm reference oracle
// must agree for every token. Seeds are every rm_flags.tsv row, so plain
// `go test` replays the seed corpus deterministically.
func FuzzRecursiveFlag(f *testing.F) {
	for _, tok := range fuzzSeedTokens(f, "rm_flags.tsv") {
		f.Add(tok)
	}

	var once sync.Once
	var match func(string) bool
	f.Fuzz(func(t *testing.T, token string) {
		if !isFlagToken(token) {
			return
		}
		once.Do(func() { match = recursiveFlagMatcher(t) })
		got := match(token)
		want := isRecursiveRmFlag(token)
		if got != want {
			t.Errorf("divergence for %q: matcher=%v reference=%v", token, got, want)
		}
	})
}

// fuzzSeedTokens reads the input column of a testdata corpus for f.Add. It
// parallels loadCorpus (which needs *testing.T) at the *testing.F level.
func fuzzSeedTokens(f *testing.F, name string) []string {
	f.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		f.Fatalf("read corpus %s: %v", name, err)
	}
	var tokens []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		input, _, ok := strings.Cut(line, "\t")
		if !ok {
			f.Fatalf("%s: missing tab separator: %q", name, line)
		}
		tokens = append(tokens, strings.ReplaceAll(input, `\t`, "\t"))
	}
	return tokens
}
