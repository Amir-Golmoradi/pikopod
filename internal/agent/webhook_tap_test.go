package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/drift"
)

type delivery struct {
	id, event, resource string
	ts                  int64
}

func runDeliveries(t *testing.T, deliveries ...delivery) (*Agent, string, *captureSink) {
	t.Helper()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	t.Cleanup(receiver.Close)
	dir := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1", AgentPort: 0, DataDir: dir,
		Upstreams: map[string]config.Upstream{"prov": {Listen: "/prov", Target: "https://api.example.invalid", Webhooks: &config.Webhooks{Receiver: receiver.URL + "/hooks"}}},
		Warmup:    config.Warmup{MinSamples: 50, MinHours: new(int)},
	}
	sink := newCaptureSink()
	a, err := New(cfg, alert.Options{MinOccurrences: 2, Window: time.Minute}, sink)
	if err != nil {
		t.Fatal(err)
	}
	startPipeline(t, a)
	front := httptest.NewServer(a.Proxy)
	t.Cleanup(front.Close)
	for _, d := range deliveries {
		body := fmt.Sprintf(`{"id":%q,"type":%q,"data":{"id":%q,"amount":100}}`, d.id, d.event, d.resource)
		req, _ := http.NewRequest("POST", front.URL+"/hooks/prov", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-pikopod-webhook-id", d.id)
		req.Header.Set("x-pikopod-webhook-event", d.event)
		req.Header.Set("x-pikopod-webhook-timestamp", fmt.Sprint(d.ts))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("delivery refused: %d", resp.StatusCode)
		}
	}
	waitFor(t, func() bool { return a.Metrics.RecordingsWritten.Load() >= int64(len(deliveries)) })
	return a, dir, sink
}

func webhookEventsOfKind(t *testing.T, dir string, kind drift.Kind) []alert.DriftEvent {
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
			t.Fatal(err)
		}
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestWebhookDuplicateFiresOnTheSecondDeliveryOfOneId(t *testing.T) {
	a, dir, sink := runDeliveries(t,
		delivery{"whd_CANARY77", "charge.succeeded", "ch_CANARY01", 1700000000},
		delivery{"whd_CANARY77", "charge.succeeded", "ch_CANARY01", 1700000005},
	)
	waitFor(t, func() bool { return len(webhookEventsOfKind(t, dir, drift.WebhookDuplicate)) >= 1 })
	evs := webhookEventsOfKind(t, dir, drift.WebhookDuplicate)
	if len(evs) != 1 || !strings.HasPrefix(evs[0].Fingerprint, "fp_") || evs[0].Field != "charge.succeeded" || evs[0].Endpoint != "/hooks/prov" {
		t.Fatalf("one duplicate incident with a fingerprint, naming the event: %+v", evs)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "events.ndjson"))
	recs, _ := os.ReadFile(filepath.Join(dir, "recordings", "prov.webhooks.ndjson"))
	if len(recs) == 0 {
		t.Fatal("deliveries are recorded in their own file")
	}
	for _, leak := range []string{"CANARY77", "CANARY01"} {
		if strings.Contains(string(raw), leak) || strings.Contains(string(recs), leak) || strings.Contains(strings.Join(sink.snapshot(), "\n"), leak) {
			t.Fatalf("CANARY LEAK %q reached disk or an alert", leak)
		}
	}
	if a.Metrics.WebhooksReceived.Load() != 2 {
		t.Fatalf("received=%d", a.Metrics.WebhooksReceived.Load())
	}
	rec := httptest.NewRecorder()
	a.healthz(rec, httptest.NewRequest("GET", "/healthz", nil))
	var health map[string]any
	json.Unmarshal(rec.Body.Bytes(), &health)
	if health["webhooks_received"] != float64(2) || health["webhooks_dropped"] != float64(0) {
		t.Fatalf("healthz must carry the tap counters: %v", health)
	}
}

func TestWebhookOutOfOrderFiresWhenAnOlderEventArrivesLater(t *testing.T) {
	_, dir, _ := runDeliveries(t,
		delivery{"whd_1", "charge.pending", "ch_CANARY02", 1700000010},
		delivery{"whd_2", "charge.succeeded", "ch_CANARY02", 1700000020},
		delivery{"whd_3", "charge.pending", "ch_CANARY02", 1700000012},
		delivery{"whd_4", "charge.pending", "ch_other", 1700000001},
	)
	waitFor(t, func() bool { return len(webhookEventsOfKind(t, dir, drift.WebhookOutOfOrder)) >= 1 })
	evs := webhookEventsOfKind(t, dir, drift.WebhookOutOfOrder)
	if len(evs) != 1 || evs[0].Field != "charge.pending" {
		t.Fatalf("one out-of-order incident for the resource that went backwards: %+v", evs)
	}
	if dup := webhookEventsOfKind(t, dir, drift.WebhookDuplicate); len(dup) != 0 {
		t.Fatalf("distinct ids are not duplicates: %+v", dup)
	}
}
