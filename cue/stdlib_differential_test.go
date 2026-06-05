package cue_test

import (
	"regexp"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// matchesRegexDef reports whether the regex-shaped definition constraintName
// accepts s, returning the boolean verdict instead of asserting it — so the
// differential test can compare two matchers' verdicts on the same input.
func matchesRegexDef(t *testing.T, ctx *cue.Context, pkg cue.Value, constraintName, s string) bool {
	t.Helper()
	cons := lookupDef(t, pkg, constraintName)
	lit := ctx.CompileString(cueStringLit(s), cue.Filename("literal.cue"))
	if err := lit.Err(); err != nil {
		t.Fatalf("literal compile error: %v", err)
	}
	return cons.Unify(lit).Validate(cue.Concrete(true)) == nil
}

// differentialWhitelist enumerates corpus inputs where #systemTarget and
// #systemInCommand legitimately disagree. Both are relative paths embedding a
// system dir after a non-word char: #systemInCommand matches the embedded
// /etc, #systemTarget rejects (no leading system component).
var differentialWhitelist = map[string]string{
	"./etc/foo": "relative path; '.' is a non-word boundary before /etc, so only systemInCommand matches",
	"../etc":    "relative path; '.' is a non-word boundary before /etc, so only systemInCommand matches",
}

// prefixBoundaryLookalike flags an absolute path whose leading component is a
// system name immediately followed by a word char (e.g. /etcfoo, /devops) —
// the bug class the differential exists to catch. The whitelist must never
// contain one, or it could silently re-absorb that bug.
var prefixBoundaryLookalike = regexp.MustCompile(`^/(etc|sys|proc|boot|dev)[A-Za-z0-9_]`)

func TestDifferential_SystemTarget_vs_SystemInCommand(t *testing.T) {
	ctx := cuecontext.New()
	pkg := loadSubPkg(t, ctx, subPkgPath)

	rows := loadCorpus(t, "system_paths.tsv")
	corpusInputs := make(map[string]bool, len(rows))
	for _, r := range rows {
		corpusInputs[r.Input] = true
	}
	for input := range differentialWhitelist {
		if !corpusInputs[input] {
			t.Errorf("stale whitelist entry %q: no matching corpus input", input)
		}
	}

	agreement := 0
	for _, r := range rows {
		target := matchesRegexDef(t, ctx, pkg, "systemTarget", r.Input)
		inCmd := matchesRegexDef(t, ctx, pkg, "systemInCommand", r.Input)
		if _, whitelisted := differentialWhitelist[r.Input]; whitelisted {
			if target || !inCmd {
				t.Errorf("whitelisted row %q no longer diverges as documented "+
					"(want systemTarget=false systemInCommand=true): got systemTarget=%v systemInCommand=%v",
					r.Input, target, inCmd)
			}
			continue
		}
		if target != inCmd {
			t.Errorf("undocumented divergence on %q: systemTarget=%v systemInCommand=%v",
				r.Input, target, inCmd)
			continue
		}
		agreement++
	}
	if agreement == 0 {
		t.Fatal("no agreement rows exercised; corpus or whitelist is wrong")
	}
}

func TestDifferential_Whitelist_NoPrefixBoundaryCase(t *testing.T) {
	for input := range differentialWhitelist {
		if prefixBoundaryLookalike.MatchString(input) {
			t.Errorf("whitelist entry %q is a prefix-boundary lookalike; "+
				"this is the bug class the differential must catch, never whitelist", input)
		}
	}
}
