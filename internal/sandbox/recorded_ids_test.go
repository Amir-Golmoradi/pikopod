package sandbox

import (
	"testing"

	"github.com/pikopod/pikopod/internal/proxy"
)

func TestARecordedIdResolvesToTheResourceTheCreateStored(t *testing.T) {
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_ids1", Seed: "ids-1", Recordings: recordedWidgets(t)})
	cred := map[string]string{"authorization": "Bearer " + e.Credential()}

	created := do(t, e, "POST", "/widgets", `{"name":"gear"}`, cred)
	if created.status != 201 || bodyOf(t, created)["id"] != "widgets_1" {
		t.Fatalf("create: %d %s", created.status, created.body)
	}
	got := do(t, e, "GET", "/widgets/wdg_tok1", "", cred)
	if got.status != 200 {
		t.Fatalf("the recorded id must resolve to the stored resource: %d %s", got.status, got.body)
	}
	if got.headers[IDRewrittenHeader] != "1" || got.headers[SourceHeader] != "" {
		t.Fatalf("the response says one id was rewritten and the store answered: %v", got.headers)
	}
	if body := bodyOf(t, got); body["id"] != "widgets_1" || body["name"] != "gear" || body["object"] != "widget" {
		t.Fatalf("the stored resource answers under its store key: %s", got.body)
	}

	patched := do(t, e, "PATCH", "/widgets/wdg_tok1", `{"name":"wdg_tok1"}`, cred)
	if patched.status != 200 || patched.headers[IDRewrittenHeader] != "2" {
		t.Fatalf("a token in the path and one in the body are both rewritten: %d %v %s", patched.status, patched.headers, patched.body)
	}
	if body := bodyOf(t, do(t, e, "GET", "/widgets/widgets_1", "", cred)); body["name"] != "widgets_1" {
		t.Fatalf("the body token became the store key before the operation ran: %v", body["name"])
	}
	plain := do(t, e, "GET", "/widgets/widgets_1", "", cred)
	if plain.headers[IDRewrittenHeader] != "" {
		t.Fatalf("a request that carries no recorded id is not marked: %v", plain.headers)
	}
}

func TestRecordedIdsSurviveANewEngineOnTheSameStore(t *testing.T) {
	store, err := OpenMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := Config{ID: "sbx_ids2", Seed: "ids-2", Recordings: recordedWidgets(t)}
	first, err := NewEngine(loadWidgets(t), cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	cred := map[string]string{"authorization": "Bearer " + first.Credential()}
	if created := do(t, first, "POST", "/widgets", `{"name":"gear"}`, cred); created.status != 201 {
		t.Fatalf("create: %d %s", created.status, created.body)
	}
	first.Close()
	second, err := NewEngine(loadWidgets(t), cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got := do(t, second, "GET", "/widgets/wdg_tok1", "", cred)
	if got.status != 200 || got.headers[IDRewrittenHeader] != "1" || bodyOf(t, got)["id"] != "widgets_1" {
		t.Fatalf("the mapping lives with the stored state, not in one process: %d %v %s", got.status, got.headers, got.body)
	}
	second.Reset()
	after := do(t, second, "GET", "/widgets/wdg_tok1", "", cred)
	if after.headers[IDRewrittenHeader] != "" {
		t.Fatalf("a reset forgets the mapping with the resources: %v", after.headers)
	}
}

func TestRecordedIdsAreBoundedAndTheOldestIsForgotten(t *testing.T) {
	was := maxRecordedIDs
	maxRecordedIDs = 2
	defer func() { maxRecordedIDs = was }()
	set := loadRecordings(t, []proxy.Record{
		{Method: "POST", Path: "/widgets", Status: 201, ReqKind: "json", ReqBody: map[string]any{"name": "a"}, RespKind: "json", RespBody: providerWidget("wdg_a", "a")},
		{Method: "POST", Path: "/widgets", Status: 201, ReqKind: "json", ReqBody: map[string]any{"name": "b"}, RespKind: "json", RespBody: providerWidget("wdg_b", "b")},
		{Method: "POST", Path: "/widgets", Status: 201, ReqKind: "json", ReqBody: map[string]any{"name": "c"}, RespKind: "json", RespBody: providerWidget("wdg_c", "c")},
	})
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_ids3", Seed: "ids-3", Recordings: set})
	cred := map[string]string{"authorization": "Bearer " + e.Credential()}
	for _, name := range []string{"a", "b", "c"} {
		if created := do(t, e, "POST", "/widgets", `{"name":"`+name+`"}`, cred); created.status != 201 {
			t.Fatalf("create %s: %d %s", name, created.status, created.body)
		}
	}
	newest := do(t, e, "GET", "/widgets/wdg_c", "", cred)
	if newest.headers[IDRewrittenHeader] != "1" || bodyOf(t, newest)["id"] != "widgets_3" {
		t.Fatalf("the newest mapping holds: %v %s", newest.headers, newest.body)
	}
	oldest := do(t, e, "GET", "/widgets/wdg_a", "", cred)
	if oldest.headers[IDRewrittenHeader] != "" {
		t.Fatalf("the oldest mapping was evicted at the bound: %v %s", oldest.headers, oldest.body)
	}
}
