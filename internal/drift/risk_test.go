package drift

import "testing"

func TestKindsAreGradedByWhatBreaksConsumers(t *testing.T) {
	want := map[Kind]Risk{
		FieldRemoved: RiskHigh, TypeChanged: RiskHigh, FieldNullable: RiskHigh, StatusNew: RiskHigh, StatusCodeChanged: RiskHigh, ErrorShapeChanged: RiskHigh,
		EnumValueNew: RiskMedium,
		FieldAdded:   RiskLow,
	}
	for kind, risk := range want {
		if got := kind.Risk(); got != risk {
			t.Errorf("%s: risk %s, want %s", kind, got, risk)
		}
	}
	if RiskHigh.Rank() <= RiskMedium.Rank() || RiskMedium.Rank() <= RiskLow.Rank() {
		t.Fatal("high outranks medium outranks low")
	}
}

func TestADocumentedFindingDropsOneTier(t *testing.T) {
	f := Finding{Kind: TypeChanged}
	if f.Risk() != RiskHigh {
		t.Fatalf("undocumented type change is high: %s", f.Risk())
	}
	f.Documented = true
	if f.Risk() != RiskMedium {
		t.Fatalf("documented type change drops to medium: %s", f.Risk())
	}
	low := Finding{Kind: FieldAdded, Documented: true}
	if low.Risk() != RiskLow {
		t.Fatalf("low stays low: %s", low.Risk())
	}
}

func TestParseRiskRefusesUnknownTiers(t *testing.T) {
	for _, ok := range []string{"high", "medium", "low"} {
		if _, err := ParseRisk(ok); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
	if _, err := ParseRisk("severe"); err == nil {
		t.Fatal("an unknown tier is refused, not guessed")
	}
}
