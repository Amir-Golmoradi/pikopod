package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/drift"
	"github.com/pikopod/pikopod/internal/importer"
	"github.com/pikopod/pikopod/internal/sandbox"
)

func accountsSpec(requireName bool) string {
	required := ""
	if requireName {
		required = `"required": ["display_name"],`
	}
	return `{
  "openapi": "3.1.0",
  "info": {"title": "Accounts", "version": "1.0.0"},
  "paths": {"/accounts": {"post": {
    "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Account"}}}},
    "responses": {"201": {"description": "created", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Account"}}}}}
  }}},
  "components": {"schemas": {"Account": {"type": "object", ` + required + ` "properties": {
    "id": {"type": "string", "readOnly": true}, "display_name": {"type": "string"}, "tier": {"type": "string"}
  }}}}
}`
}

func forkFactory(t *testing.T, spec string) ForkFactory {
	t.Helper()
	def, err := importer.NormalizeOpenAPI([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	return func(upstream string) (*sandbox.Engine, func(), error) {
		if upstream != "prov" {
			return nil, nil, fmt.Errorf("no sandbox for %s", upstream)
		}
		st, err := sandbox.OpenMemoryStore()
		if err != nil {
			return nil, nil, err
		}
		eng, err := sandbox.NewEngine(def, sandbox.Config{ID: "sbx_fork", Seed: "fork-1", Mode: "deterministic"}, st)
		if err != nil {
			st.Close()
			return nil, nil, err
		}
		return eng, func() { eng.Close(); st.Close() }, nil
	}
}

func runCreates(t *testing.T, spec string, answer func(call int32, w http.ResponseWriter), bodies ...string) (*Agent, string, *captureSink) {
	t.Helper()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		answer(calls.Add(1), w)
	}))
	t.Cleanup(upstream.Close)
	dir := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1", AgentPort: 0, DataDir: dir,
		Upstreams: map[string]config.Upstream{"prov": {Listen: "/prov", Target: upstream.URL}},
		Warmup:    config.Warmup{MinSamples: 50, MinHours: new(int)},
	}
	sink := newCaptureSink()
	a, err := New(cfg, alert.Options{MinOccurrences: 2, Window: time.Minute}, sink)
	if err != nil {
		t.Fatal(err)
	}
	a.SetSandboxFork(forkFactory(t, spec))
	startPipeline(t, a)
	front := httptest.NewServer(a.Proxy)
	t.Cleanup(front.Close)
	for _, body := range bodies {
		resp, err := http.Post(front.URL+"/prov/accounts", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	waitFor(t, func() bool { return a.Metrics.RecordingsWritten.Load() >= int64(len(bodies)) })
	return a, dir, sink
}

func divergenceEvents(t *testing.T, dir string) []alert.DriftEvent {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "events.ndjson"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []alert.DriftEvent
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var ev alert.DriftEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("event log line not schema-shaped: %v", err)
		}
		if ev.Kind == drift.BehaviourDivergence {
			out = append(out, ev)
		}
	}
	return out
}

func TestDivergenceReportsARefusalTheSandboxWouldNotHaveMade(t *testing.T) {
	a, dir, sink := runCreates(t, accountsSpec(false), func(call int32, w http.ResponseWriter) {
		if call == 1 {
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"acc_CANARY0001","display_name":"Acme Ltd","tier":"gold"}`)
			return
		}
		w.WriteHeader(422)
		fmt.Fprint(w, `{"error":"display_name already taken"}`)
	}, `{"display_name":"Acme Ltd","tier":"gold"}`, `{"display_name":"Acme Ltd","tier":"gold"}`)
	waitFor(t, func() bool { return len(divergenceEvents(t, dir)) >= 1 })
	evs := divergenceEvents(t, dir)
	if len(evs) != 1 {
		t.Fatalf("exactly one divergence event: %+v", evs)
	}
	ev := evs[0]
	if ev.Category != "undocumented_rule" || ev.Endpoint != "/accounts" || ev.Method != "POST" || ev.Before != "201" || ev.After != "422" {
		t.Fatalf("event: %+v", ev)
	}
	if !strings.Contains(ev.Detail, "body.display_name") || !strings.Contains(ev.Field, "body.display_name") {
		t.Fatalf("the request features that differed are named: %+v", ev)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "events.ndjson"))
	for _, leak := range []string{"Acme Ltd", "Acme", "CANARY0001", "already taken"} {
		if strings.Contains(string(raw), leak) || strings.Contains(strings.Join(sink.snapshot(), "\n"), leak) {
			t.Fatalf("CANARY LEAK %q in the event log or alert", leak)
		}
	}
	if a.DivergenceChecked.Load() != 2 || a.DivergenceUnverifiable.Load() != 0 {
		t.Fatalf("checked=%d unverifiable=%d", a.DivergenceChecked.Load(), a.DivergenceUnverifiable.Load())
	}
	joined := strings.Join(sink.snapshot(), "\n")
	if !strings.Contains(joined, "divergence") || !strings.Contains(joined, "reproduce "+ev.Fingerprint) {
		t.Fatalf("the alert names the class and the reproduce handle:\n%s", joined)
	}
}

func TestDivergenceStaysQuietWhenTheProviderAgreesWithTheSandbox(t *testing.T) {
	a, dir, _ := runCreates(t, accountsSpec(false), func(call int32, w http.ResponseWriter) {
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"id":"acc_%08d","display_name":"Acme Ltd","tier":"gold"}`, call)
	}, `{"display_name":"Acme Ltd","tier":"gold"}`, `{"display_name":"Other Co","tier":"free"}`)
	waitFor(t, func() bool { return a.DivergenceChecked.Load() >= 2 })
	if evs := divergenceEvents(t, dir); len(evs) != 0 {
		t.Fatalf("no divergence when the shapes agree: %+v", evs)
	}
}

func TestDivergenceCountsRedactedEvidenceAsUnverifiable(t *testing.T) {
	a, dir, _ := runCreates(t, accountsSpec(true), func(call int32, w http.ResponseWriter) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":"display_name must not contain spaces"}`)
	}, `{"display_name":"Acme Ltd"}`)
	waitFor(t, func() bool { return a.DivergenceUnverifiable.Load() >= 1 })
	if evs := divergenceEvents(t, dir); len(evs) != 0 {
		t.Fatalf("redacted evidence never becomes an event: %+v", evs)
	}
	if a.DivergenceChecked.Load() != 1 {
		t.Fatalf("checked=%d", a.DivergenceChecked.Load())
	}
	rec := httptest.NewRecorder()
	a.healthz(rec, httptest.NewRequest("GET", "/healthz", nil))
	var health map[string]any
	json.Unmarshal(rec.Body.Bytes(), &health)
	if health["divergence_checked"] != float64(1) || health["divergence_unverifiable"] != float64(1) {
		t.Fatalf("healthz must carry both counters: %v", health)
	}
}
