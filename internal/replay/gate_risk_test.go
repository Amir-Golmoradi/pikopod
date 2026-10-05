package replay

import (
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/baseline"
	"github.com/pikopod/pikopod/internal/drift"
	"github.com/pikopod/pikopod/internal/proxy"
)

func frozenBaseline(t *testing.T, dir string) {
	t.Helper()
	l := baseline.NewLearner("pay", dir, baseline.Warmup{MinSamples: 5, MinAge: 0})
	ts := time.Now()
	for i := 0; i < 8; i++ {
		l.Observe("GET", "/charge/tx_00000000000"+string(rune('a'+i)), 200, body(`{"id":"x1","status":"success","amount":100}`), ts)
	}
	if err := l.Persist(); err != nil {
		t.Fatal(err)
	}
}

func kinds(fs []GateFinding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Kind)
	}
	return out
}

func TestGateFailsOnTheTierAndAcceptsBelowIt(t *testing.T) {
	dir := t.TempDir()
	frozenBaseline(t, dir)
	writeRecordings(t, dir, "pay", []proxy.Record{
		{Method: "GET", Path: "/charge/tx_aaa111bbb222", Status: 200, RespBody: body(`{"id":"x1","status":"success","amount":"100","fee_bearer":"merchant"}`), RespKind: "json"},
	})
	for _, tier := range []drift.Risk{drift.RiskMedium, drift.RiskHigh} {
		res, err := GateWith(dir, "pay", nil, GateOptions{FailOn: tier, Accepted: map[string]bool{}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Findings) != 1 || res.Findings[0].Kind != "type_changed" || res.Findings[0].Risk != "high" {
			t.Fatalf("at %s the type change fails the gate: %v", tier, kinds(res.Findings))
		}
		if len(res.Accepted) != 1 || res.Accepted[0].Kind != "field_added" || res.Accepted[0].Risk != "low" {
			t.Fatalf("at %s the added field is accepted: %+v", tier, res.Accepted)
		}
		if res.Accepted[0].Because == "" || res.Findings[0].Fingerprint == "" {
			t.Fatalf("every finding carries a fingerprint and every acceptance a reason: %+v %+v", res.Findings[0], res.Accepted[0])
		}
	}
	res, err := GateWith(dir, "pay", nil, GateOptions{FailOn: drift.RiskLow, Accepted: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 2 || len(res.Accepted) != 0 {
		t.Fatalf("at low everything fails: %v accepted %v", kinds(res.Findings), kinds(res.Accepted))
	}
}

func TestAnAdditiveFieldPassesAtMediumAndFailsAtLow(t *testing.T) {
	dir := t.TempDir()
	frozenBaseline(t, dir)
	writeRecordings(t, dir, "pay", []proxy.Record{
		{Method: "GET", Path: "/charge/tx_aaa111bbb222", Status: 200, RespBody: body(`{"id":"x1","status":"success","amount":100,"fee_bearer":"merchant"}`), RespKind: "json"},
	})
	res, err := GateWith(dir, "pay", nil, GateOptions{FailOn: drift.RiskMedium, Accepted: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 || len(res.Accepted) != 1 {
		t.Fatalf("medium accepts an added field: %v accepted %v", kinds(res.Findings), kinds(res.Accepted))
	}
	res, err = GateWith(dir, "pay", nil, GateOptions{FailOn: drift.RiskLow, Accepted: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || len(res.Accepted) != 0 {
		t.Fatalf("low fails on an added field: %v", kinds(res.Findings))
	}
	if res.FailOn != "low" {
		t.Fatalf("the result says which tier it was judged at: %q", res.FailOn)
	}
}

func TestAnAcceptedFingerprintIsExcluded(t *testing.T) {
	dir := t.TempDir()
	frozenBaseline(t, dir)
	writeRecordings(t, dir, "pay", []proxy.Record{
		{Method: "GET", Path: "/charge/tx_aaa111bbb222", Status: 200, RespBody: body(`{"id":"x1","status":"success","amount":"100"}`), RespKind: "json"},
	})
	first, err := GateWith(dir, "pay", nil, GateOptions{FailOn: drift.RiskMedium, Accepted: map[string]bool{}})
	if err != nil || len(first.Findings) != 1 {
		t.Fatalf("one failing finding first: %v %+v", err, first)
	}
	fp := first.Findings[0].Fingerprint
	res, err := GateWith(dir, "pay", nil, GateOptions{FailOn: drift.RiskMedium, Accepted: map[string]bool{fp: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 || len(res.Accepted) != 1 || res.Accepted[0].Fingerprint != fp {
		t.Fatalf("an accepted fingerprint moves to accepted: %+v", res)
	}
	if res.Accepted[0].Because != "fingerprint accepted" {
		t.Fatalf("the reason names the acceptance: %q", res.Accepted[0].Because)
	}
}
