package diag

import "testing"

// TestE0504_Registered pins that CRP-005 promotes the interim duplicate-rule
// guard to a first-class diagnostic: E0504 must resolve through LookupCode with
// a non-empty Help string, the same shape as every other registered code.
func TestE0504_Registered(t *testing.T) {
	info, ok := LookupCode("E0504")
	if !ok {
		t.Fatalf("LookupCode(%q) should resolve once CRP-005 registers it; got ok=false", "E0504")
	}
	if info.Code != "E0504" {
		t.Errorf("CodeInfo.Code = %q, want %q", info.Code, "E0504")
	}
	if info.Help == "" {
		t.Errorf("E0504 must carry a non-empty Help string; got empty")
	}
}

// TestE0504_BumpsCodesInScope pins that registering E0504 advances the frozen
// in-scope code count from 16 to 17 (E0505 is already counted).
func TestE0504_BumpsCodesInScope(t *testing.T) {
	if CodesInScopeV1 != 18 {
		t.Errorf("CodesInScopeV1 = %d, want 18 after E0504 joins the registry", CodesInScopeV1)
	}
}
