package bridge

import (
	"testing"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/drift"
	"github.com/pikopod/pikopod/internal/proxy"
)

func TestBuildFromRecordArmsADivergence(t *testing.T) {
	ev := &alert.DriftEvent{Fingerprint: "fp_0123456789ab", Upstream: "pay", Method: "POST", Endpoint: "/accounts", Kind: drift.BehaviourDivergence, Category: "undocumented_rule", After: "422"}
	rec := &proxy.Record{Upstream: "pay", Method: "POST", Path: "/accounts", Status: 422, ReqKind: "json", ReqBody: map[string]any{"tier": "gold"}, RespKind: "json", RespBody: map[string]any{"code": "duplicate"}}
	name, pack, err := BuildFromRecord(ev, rec, 0)
	if err != nil {
		t.Fatalf("a divergence reproduces like an incident: %v", err)
	}
	if name == "" || pack == nil {
		t.Fatalf("pack: %s %v", name, pack)
	}
	accepted := &alert.DriftEvent{Fingerprint: "fp_0123456789ac", Upstream: "pay", Method: "POST", Endpoint: "/accounts", Kind: drift.BehaviourDivergence, Category: "spec_drift", After: "201"}
	if _, _, err := BuildFromRecord(accepted, &proxy.Record{Method: "POST", Path: "/accounts", Status: 201, RespKind: "json", RespBody: map[string]any{}}, 0); err == nil {
		t.Fatal("a success the sandbox refused has nothing to arm yet; refuse with the reason")
	}
}
