package diag_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/srnnkls/fas/internal/diag"
)

// expectedCodes enumerates every error code the registry must declare.
// Hardcoded here (not read from codes.go) to prevent oracle mirroring:
// the test encodes the SPEC (design.md error code table), not the source.
type expectedCode struct {
	code  string // "E0201"
	class string // human-readable range name for failure messages
	varFn func() diag.CodeInfo
}

func expectedCodes() []expectedCode {
	return []expectedCode{
		// E01xx — rule load
		{"E0101", "rule load", func() diag.CodeInfo { return diag.E0101 }},
		{"E0102", "rule load", func() diag.CodeInfo { return diag.E0102 }},
		{"E0103", "rule load", func() diag.CodeInfo { return diag.E0103 }},
		// E02xx — path resolution
		{"E0201", "path resolution", func() diag.CodeInfo { return diag.E0201 }},
		{"E0202", "path resolution", func() diag.CodeInfo { return diag.E0202 }},
		{"E0203", "path resolution", func() diag.CodeInfo { return diag.E0203 }},
		// E03xx — leaf constraint
		{"E0301", "leaf constraint", func() diag.CodeInfo { return diag.E0301 }},
		{"E0302", "leaf constraint", func() diag.CodeInfo { return diag.E0302 }},
		{"E0303", "leaf constraint", func() diag.CodeInfo { return diag.E0303 }},
		{"E0304", "leaf constraint", func() diag.CodeInfo { return diag.E0304 }},
		// E04xx — disjunction
		{"E0401", "disjunction", func() diag.CodeInfo { return diag.E0401 }},
		{"E0402", "disjunction", func() diag.CodeInfo { return diag.E0402 }},
		// E05xx — scope / binding
		{"E0501", "scope/binding", func() diag.CodeInfo { return diag.E0501 }},
		{"E0502", "scope/binding", func() diag.CodeInfo { return diag.E0502 }},
		{"E0503", "scope/binding", func() diag.CodeInfo { return diag.E0503 }},
		{"E0504", "scope/binding", func() diag.CodeInfo { return diag.E0504 }},
		{"E0505", "scope/binding", func() diag.CodeInfo { return diag.E0505 }},
		{"E0506", "scope/binding", func() diag.CodeInfo { return diag.E0506 }},
		{"E0507", "scope/binding", func() diag.CodeInfo { return diag.E0507 }},
		{"E0508", "scope/binding", func() diag.CodeInfo { return diag.E0508 }},
		// E06xx — lattice binding
		{"E0601", "lattice binding", func() diag.CodeInfo { return diag.E0601 }},
	}
}

// Every declared code has a non-empty Help string.
// Catches stubbed "TODO" entries at review time.
func TestAllCodesHaveNonEmptyHelp(t *testing.T) {
	for _, ec := range expectedCodes() {
		t.Run(ec.code, func(t *testing.T) {
			info := ec.varFn()
			if strings.TrimSpace(info.Help) == "" {
				t.Errorf("%s: Help is empty or whitespace-only; every code must ship with an explanatory help string", ec.code)
			}
		})
	}
}

// Each package-level code var's Code field matches its variable name.
// Catches copy-paste bugs like `var E0202 = CodeInfo{Code: "E0201", ...}`.
func TestCodeStringMatchesVariableName(t *testing.T) {
	for _, ec := range expectedCodes() {
		t.Run(ec.code, func(t *testing.T) {
			info := ec.varFn()
			if info.Code != ec.code {
				t.Errorf("%s.Code = %q, want %q", ec.code, info.Code, ec.code)
			}
		})
	}
}

// Codes fall within the documented ranges defined by the design.
// E01xx → rule load, E02xx → path resolution, etc.
func TestCodesFallWithinDocumentedRanges(t *testing.T) {
	pattern := regexp.MustCompile(`^E(0[1-6])\d{2}$`)
	for _, ec := range expectedCodes() {
		t.Run(ec.code, func(t *testing.T) {
			if !pattern.MatchString(ec.code) {
				t.Errorf("%s does not match the documented E0[1-5]xx range pattern", ec.code)
			}
			// Sanity: derive the prefix from the test's own expected string
			// and confirm the code string uses it (guards against a future
			// regression where a code is renumbered outside its class).
			wantPrefix := ec.code[:3] // "E01" / "E02" / ...
			info := ec.varFn()
			if !strings.HasPrefix(info.Code, wantPrefix) {
				t.Errorf("%s (%s class) Code = %q, want prefix %q",
					ec.code, ec.class, info.Code, wantPrefix)
			}
		})
	}
}

// Looking up a declared code by string returns the same CodeInfo as the
// package-level var, with ok=true.
func TestLookupCodeReturnsDeclaredCode(t *testing.T) {
	for _, ec := range expectedCodes() {
		t.Run(ec.code, func(t *testing.T) {
			got, ok := diag.LookupCode(ec.code)
			if !ok {
				t.Fatalf("LookupCode(%q): ok = false, want true", ec.code)
			}
			want := ec.varFn()
			if got.Code != want.Code {
				t.Errorf("LookupCode(%q).Code = %q, want %q",
					ec.code, got.Code, want.Code)
			}
			if got.Help != want.Help {
				t.Errorf("LookupCode(%q).Help differs from package var %s.Help",
					ec.code, ec.code)
			}
		})
	}
}

// Looking up an unknown code returns the zero CodeInfo and ok=false.
func TestLookupCodeUnknownReturnsZeroFalse(t *testing.T) {
	cases := []string{
		"E9999",  // clearly outside declared ranges
		"E0104",  // inside E01xx range but not declared
		"",       // empty
		"e0201",  // wrong case
		"E0201x", // trailing garbage
		"not-a-code",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			got, ok := diag.LookupCode(c)
			if ok {
				t.Errorf("LookupCode(%q): ok = true, want false", c)
			}
			var zero diag.CodeInfo
			if got != zero {
				t.Errorf("LookupCode(%q): got = %+v, want zero value %+v",
					c, got, zero)
			}
		})
	}
}

// No two declared codes share the same Code string.
// Catches copy-paste duplication where two vars end up with the same "E0201".
func TestNoDuplicateCodes(t *testing.T) {
	seen := make(map[string]string) // code string -> first var name seen
	for _, ec := range expectedCodes() {
		info := ec.varFn()
		if prev, dup := seen[info.Code]; dup {
			t.Errorf("duplicate code %q declared by both %s and %s",
				info.Code, prev, ec.code)
			continue
		}
		seen[info.Code] = ec.code
	}
}

// Help strings are multi-line (at least one newline) per design spec:
// "Each code entry has a short Help string (3-5 lines)".
// Rough but catches single-line stubs like "TODO" or "schema mismatch".
func TestHelpIsMultiLine(t *testing.T) {
	for _, ec := range expectedCodes() {
		t.Run(ec.code, func(t *testing.T) {
			info := ec.varFn()
			if !strings.Contains(info.Help, "\n") {
				t.Errorf("%s.Help contains no newline (len=%d); design requires 3-5 lines of explanation, got: %q",
					ec.code, len(info.Help), info.Help)
			}
		})
	}
}

// CodeInfo zero value has empty Code and Help — guards the contract that
// LookupCode's unknown-case returns something meaningful (not an accidentally
// populated default).
func TestCodeInfoZeroValue(t *testing.T) {
	var zero diag.CodeInfo
	if zero.Code != "" {
		t.Errorf("zero CodeInfo.Code = %q, want empty string", zero.Code)
	}
	if zero.Help != "" {
		t.Errorf("zero CodeInfo.Help = %q, want empty string", zero.Help)
	}
}

// Asserts that this scope adds no new error codes.
func TestNoNewCodesInScope(t *testing.T) {
	const frozen = 21
	if got := diag.CodesInScopeV1; got != frozen {
		t.Errorf("diag.CodesInScopeV1 = %d, want %d", got, frozen)
	}
	if got := len(expectedCodes()); got != frozen {
		t.Errorf("expectedCodes() enumerated %d codes, want %d", got, frozen)
	}
}

// E0301 help must not promise a `want:` label for literal regex constraints.
func TestE0301HelpReflectsWantSuppression(t *testing.T) {
	const misleading = "(`want`)"
	if strings.Contains(diag.E0301.Help, misleading) {
		t.Errorf("E0301.Help contains misleading substring %q", misleading)
	}
}
