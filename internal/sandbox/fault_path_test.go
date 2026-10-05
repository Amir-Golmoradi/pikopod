package sandbox

import "testing"

func TestArmedFaultMatchesALearnedTemplateAndAConcretePath(t *testing.T) {
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_fp", Seed: "fp-1"})
	cred := map[string]string{"authorization": "Bearer " + e.Credential()}
	e.ArmFault(FaultRule{Kind: "error", Status: 503, Method: "GET", Path: "/widgets/w_{id}", Probability: 1})
	if got := do(t, e, "GET", "/widgets/w_8c1f2a", "", cred); got.status != 503 {
		t.Fatalf("a fault armed on the template the agent learned fires on the matching path: %d", got.status)
	}
	if got := do(t, e, "GET", "/widgets/other", "", cred); got.status == 503 {
		t.Fatal("a path outside the learned template is untouched")
	}
	e.ClearFaults("", "")
	e.ArmFault(FaultRule{Kind: "error", Status: 503, Method: "GET", Path: "/widgets/w_8c1f2a", Probability: 1})
	if got := do(t, e, "GET", "/widgets/w_8c1f2a", "", cred); got.status != 503 {
		t.Fatalf("a fault armed on a concrete path fires on that path: %d", got.status)
	}
	if got := do(t, e, "GET", "/widgets/w_other", "", cred); got.status == 503 {
		t.Fatal("a concrete path fault does not fire elsewhere")
	}
}
