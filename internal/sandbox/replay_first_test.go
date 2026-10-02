package sandbox

import (
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/replay"
)

func providerWidget(id, name string) map[string]any {
	return map[string]any{"id": id, "object": "widget", "name": name, "livemode": false, "createdAt": "2025-01-01T00:00:00Z"}
}

func recordedWidgets(t *testing.T) *replay.Set {
	t.Helper()
	return loadRecordings(t, []proxy.Record{
		{Method: "POST", Path: "/widgets", Status: 201, ReqKind: "json", ReqBody: map[string]any{"name": "gear"}, RespKind: "json", RespBody: providerWidget("wdg_tok1", "gear")},
		{Method: "GET", Path: "/widgets/wdg_tok1", Status: 200, RespKind: "json", RespBody: providerWidget("wdg_tok1", "gear")},
		{Method: "POST", Path: "/widgets", Status: 422, ReqKind: "json", ReqBody: map[string]any{"name": ""}, RespKind: "json", RespBody: map[string]any{"error": "name is required"}},
		{Method: "GET", Path: "/balance", Status: 200, RespKind: "json", RespBody: map[string]any{"available": float64(5000)}},
	})
}

func TestRecordingAnswersWhatTheStoreCannotAndTheStoreAnswersItsOwn(t *testing.T) {
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_rf1", Seed: "rf-1", Recordings: recordedWidgets(t)})
	cred := map[string]string{"authorization": "Bearer " + e.Credential()}

	got := do(t, e, "GET", "/widgets/wdg_tok1", "", cred)
	if got.status != 200 || got.headers[SourceHeader] != "recorded" {
		t.Fatalf("an id the store does not hold must be answered from the recording: %d %v %s", got.status, got.headers, got.body)
	}
	if body := bodyOf(t, got); body["id"] != "wdg_tok1" || body["livemode"] != false {
		t.Fatalf("the recorded body must serve as stored: %s", got.body)
	}

	created := do(t, e, "POST", "/widgets", `{"name":"own"}`, cred)
	if created.status != 201 {
		t.Fatalf("create: %d %s", created.status, created.body)
	}
	id, _ := bodyOf(t, created)["id"].(string)
	own := do(t, e, "GET", "/widgets/"+id, "", cred)
	if own.status != 200 || own.headers[SourceHeader] != "" {
		t.Fatalf("a resource the sandbox created is answered by the store, not a recording: %d %v", own.status, own.headers)
	}
	if body := bodyOf(t, own); body["name"] != "own" {
		t.Fatalf("stored resource must win: %s", own.body)
	}
}

func TestCreateMatchingARecordingTakesTheProviderShape(t *testing.T) {
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_rf2", Seed: "rf-2", Recordings: recordedWidgets(t)})
	cred := map[string]string{"authorization": "Bearer " + e.Credential()}

	created := do(t, e, "POST", "/widgets", `{"name":"gear"}`, cred)
	if created.status != 201 || created.headers[SourceHeader] != "recorded" {
		t.Fatalf("a create matching a recording runs the operation and says the recording shaped it: %d %v", created.status, created.headers)
	}
	body := bodyOf(t, created)
	if body["id"] != "widgets_1" || body["object"] != "widget" || body["livemode"] != false || body["name"] != "gear" || body["createdAt"] != "2025-01-01T00:00:00Z" {
		t.Fatalf("the stored resource is completed from the recorded body with the store key as id: %s", created.body)
	}
	back := do(t, e, "GET", "/widgets/widgets_1", "", cred)
	if back.status != 200 {
		t.Fatalf("read-back: %d %s", back.status, back.body)
	}
	if again := bodyOf(t, back); again["object"] != "widget" || again["livemode"] != false || again["id"] != "widgets_1" {
		t.Fatalf("read-back must return the provider's shape: %s", back.body)
	}
}

func TestShapeOnlyMatchNeverServesARecordedError(t *testing.T) {
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_rf3", Seed: "rf-3", Recordings: recordedWidgets(t)})
	cred := map[string]string{"authorization": "Bearer " + e.Credential()}

	fine := do(t, e, "POST", "/widgets", `{"name":"fine"}`, cred)
	if fine.status != 201 {
		t.Fatalf("a body that only shares its shape with a refused one must not inherit the refusal: %d %s", fine.status, fine.body)
	}
	exact := do(t, e, "POST", "/widgets", `{"name":""}`, cred)
	if exact.status != 422 || exact.headers[SourceHeader] != "recorded" {
		t.Fatalf("the exact body the provider refused is refused the same way: %d %v %s", exact.status, exact.headers, exact.body)
	}
	if body := bodyOf(t, exact); body["error"] != "name is required" {
		t.Fatalf("recorded error body must serve verbatim: %s", exact.body)
	}
}

func TestRecordedFieldsAreNotedAndTheResponseSaysSo(t *testing.T) {
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_rf4", Seed: "rf-4", Recordings: recordedWidgets(t)})
	cred := map[string]string{"authorization": "Bearer " + e.Credential()}
	var lines []string
	e.SetTrace(func(stage, message string) {
		if stage == "synth" {
			lines = append(lines, message)
		}
	})

	got := do(t, e, "GET", "/widgets/wdg_tok1", "", cred)
	if got.headers[SourceHeader] != "recorded" {
		t.Fatalf("header: %v", got.headers)
	}
	joined := strings.Join(lines, "\n")
	for _, field := range []string{"id", "object", "name", "livemode", "createdAt"} {
		if !strings.Contains(joined, field+" ← recorded") {
			t.Fatalf("every served field is noted recorded, missing %q in:\n%s", field, joined)
		}
	}

	lines = nil
	created := do(t, e, "POST", "/widgets", `{"name":"gear"}`, cred)
	if created.headers[SourceHeader] != "recorded" {
		t.Fatalf("create header: %v", created.headers)
	}
	joined = strings.Join(lines, "\n")
	for _, want := range []string{"object ← recorded", "livemode ← recorded", "createdAt ← recorded", "id ← store key widgets_1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestRecordingsModeOffAndFallbackKeepTheOldPrecedence(t *testing.T) {
	off := newEngine(t, loadWidgets(t), Config{ID: "sbx_rf5", Seed: "rf-5", Recordings: recordedWidgets(t), RecordingsMode: "off"})
	cred := map[string]string{"authorization": "Bearer " + off.Credential()}
	if got := do(t, off, "GET", "/widgets/wdg_tok1", "", cred); got.status != 404 || got.headers[SourceHeader] != "" {
		t.Fatalf("off: pure synthesis, nothing recorded serves: %d %v", got.status, got.headers)
	}
	if got := do(t, off, "GET", "/balance", "", cred); got.status != 404 {
		t.Fatalf("off: no fallback either: %d", got.status)
	}
	created := do(t, off, "POST", "/widgets", `{"name":"gear"}`, cred)
	if _, has := bodyOf(t, created)["livemode"]; has || created.headers[SourceHeader] != "" {
		t.Fatalf("off: a create must not borrow the recorded shape: %s", created.body)
	}

	fallback := newEngine(t, loadWidgets(t), Config{ID: "sbx_rf6", Seed: "rf-6", Recordings: recordedWidgets(t), RecordingsMode: "fallback"})
	cred = map[string]string{"authorization": "Bearer " + fallback.Credential()}
	if got := do(t, fallback, "GET", "/widgets/wdg_tok1", "", cred); got.status != 404 || got.headers[SourceHeader] != "" {
		t.Fatalf("fallback: a declared route is never shadowed by a recording: %d %v", got.status, got.headers)
	}
	if got := do(t, fallback, "GET", "/balance", "", cred); got.status != 200 || got.headers[ReplayTierHeader] == "" || got.headers[SourceHeader] != "recorded" {
		t.Fatalf("fallback: an unroutable path is answered from the recording: %d %v", got.status, got.headers)
	}
}
