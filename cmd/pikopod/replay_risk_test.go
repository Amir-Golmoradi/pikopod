package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/baseline"
	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/proxy"
)

func gateFixture(t *testing.T, cfg *config.Config, respBody string) {
	t.Helper()
	l := baseline.NewLearner("fake", cfg.DataDir, baseline.Warmup{MinSamples: 5, MinAge: 0})
	ts := time.Now()
	var ref any
	json.Unmarshal([]byte(`{"id":"x1","status":"success","amount":100}`), &ref)
	for i := 0; i < 8; i++ {
		l.Observe("GET", "/charge/tx_00000000000"+string(rune('a'+i)), 200, ref, ts)
	}
	if err := l.Persist(); err != nil {
		t.Fatal(err)
	}
	var body any
	json.Unmarshal([]byte(respBody), &body)
	rdir := filepath.Join(cfg.DataDir, "recordings")
	os.MkdirAll(rdir, 0o700)
	raw, _ := json.Marshal(proxy.Record{TS: ts, Upstream: "fake", Method: "GET", Path: "/charge/tx_aaa111bbb222", Status: 200, RespKind: "json", RespBody: body})
	if err := os.WriteFile(filepath.Join(rdir, "fake.ndjson"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReplayCIFailOnTiers(t *testing.T) {
	cfg := testConfig(t, "https://example.invalid")
	gateFixture(t, cfg, `{"id":"x1","status":"success","amount":"100","fee_bearer":"merchant"}`)

	res, _ := callTool(t, cfg, "replay_ci", map[string]any{})
	if res["verdict"] != VerdictFindings {
		t.Fatalf("a type change fails at the default tier: %v", res)
	}
	ups, _ := res["data"].(map[string]any)["upstreams"].(map[string]any)
	fake, _ := ups["fake"].(map[string]any)
	findings, _ := fake["findings"].([]any)
	accepted, _ := fake["accepted"].([]any)
	if len(findings) != 1 || len(accepted) != 1 || fake["fail_on"] != "medium" {
		t.Fatalf("one failing, one accepted, judged at medium: %v", fake)
	}
	if f, _ := findings[0].(map[string]any); f["kind"] != "type_changed" || f["risk"] != "high" {
		t.Fatalf("the failing finding is the high one: %v", findings[0])
	}

	res, _ = callTool(t, cfg, "replay_ci", map[string]any{"fail_on": "low"})
	ups, _ = res["data"].(map[string]any)["upstreams"].(map[string]any)
	fake, _ = ups["fake"].(map[string]any)
	if findings, _ := fake["findings"].([]any); len(findings) != 2 {
		t.Fatalf("at low both fail: %v", fake)
	}

	res, _ = callTool(t, cfg, "replay_ci", map[string]any{"fail_on": "severe"})
	if res["verdict"] != VerdictError {
		t.Fatalf("an unknown tier is an ERROR, not a guess: %v", res)
	}
}

func TestReplayCIAdditiveOnlyPassesAtMedium(t *testing.T) {
	cfg := testConfig(t, "https://example.invalid")
	gateFixture(t, cfg, `{"id":"x1","status":"success","amount":100,"fee_bearer":"merchant"}`)
	res, _ := callTool(t, cfg, "replay_ci", map[string]any{})
	if res["verdict"] != VerdictClean {
		t.Fatalf("an added field alone is CLEAN at medium: %v", res)
	}
	ups, _ := res["data"].(map[string]any)["upstreams"].(map[string]any)
	fake, _ := ups["fake"].(map[string]any)
	if accepted, _ := fake["accepted"].([]any); len(accepted) != 1 {
		t.Fatalf("the accepted finding is still reported: %v", fake)
	}
	res, _ = callTool(t, cfg, "replay_ci", map[string]any{"fail_on": "low"})
	if res["verdict"] != VerdictFindings {
		t.Fatalf("at low it fails: %v", res)
	}
}

func TestReplayCIHonoursAcceptedFingerprints(t *testing.T) {
	cfg := testConfig(t, "https://example.invalid")
	gateFixture(t, cfg, `{"id":"x1","status":"success","amount":"100"}`)
	res, _ := callTool(t, cfg, "replay_ci", map[string]any{})
	ups, _ := res["data"].(map[string]any)["upstreams"].(map[string]any)
	fake, _ := ups["fake"].(map[string]any)
	findings, _ := fake["findings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("one finding before accepting: %v", fake)
	}
	fp, _ := findings[0].(map[string]any)["fingerprint"].(string)
	if !strings.HasPrefix(fp, "fp_") {
		t.Fatalf("findings carry the fingerprint accept takes: %v", findings[0])
	}
	os.MkdirAll(filepath.Join(cfg.DataDir, "alerts"), 0o700)
	os.WriteFile(filepath.Join(cfg.DataDir, "alerts", "state.json"), []byte(`{"`+fp+`":{"acked":true}}`), 0o600)
	res, _ = callTool(t, cfg, "replay_ci", map[string]any{})
	if res["verdict"] != VerdictClean {
		t.Fatalf("an accepted fingerprint no longer fails the gate: %v", res)
	}
	ups, _ = res["data"].(map[string]any)["upstreams"].(map[string]any)
	fake, _ = ups["fake"].(map[string]any)
	if accepted, _ := fake["accepted"].([]any); len(accepted) != 1 || accepted[0].(map[string]any)["because"] != "fingerprint accepted" {
		t.Fatalf("it is reported as accepted with the reason: %v", fake)
	}
}

func TestReplayCLIRefusesAnUnknownTier(t *testing.T) {
	cliDir(t)
	_, err := runCLI(t, newReplayCmd(), "--ci", "--fail-on", "severe")
	if err == nil || !strings.Contains(err.Error(), "high, medium or low") {
		t.Fatalf("--fail-on severe is refused with the choices: %v", err)
	}
}
