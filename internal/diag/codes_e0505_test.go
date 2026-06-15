package diag

import "testing"

// TestDiag_E0505_Registered pins that CRP-001 registers the new package-clause
// diagnostic E0505 in the code registry with a non-empty Help string.
//
// FAILS today: E0505 is not registered, LookupCode returns ok=false.
func TestDiag_E0505_Registered(t *testing.T) {
	info, ok := LookupCode("E0505")
	if !ok {
		t.Fatal("LookupCode(\"E0505\") should resolve; E0505 is not registered")
	}
	if info.Code != "E0505" {
		t.Errorf("info.Code = %q, want %q", info.Code, "E0505")
	}
	if info.Help == "" {
		t.Error("E0505 must carry a non-empty Help string")
	}
}

// TestDiag_CodesInScopeV1_IncludesE0505 pins the frozen code count after
// CRP-001 adds exactly one code (E0505). A sibling task adds E0504 later, so
// this bumps 15 -> 16, not 17.
//
// FAILS today: CodesInScopeV1 == 15.
func TestDiag_CodesInScopeV1_IncludesE0505(t *testing.T) {
	if CodesInScopeV1 != 16 {
		t.Errorf("CodesInScopeV1 = %d, want 16 (CRP-001 adds only E0505)", CodesInScopeV1)
	}
}
