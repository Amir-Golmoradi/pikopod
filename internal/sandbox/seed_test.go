package sandbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSeedStoresValidatedResourcesThatReadsAnswer(t *testing.T) {
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_seed", Seed: "seed-1"})
	n, err := e.Seed(map[string][]map[string]any{
		"widgets": {{"id": "w_1", "name": "gear"}, {"name": "cog"}},
	})
	if err != nil || n != 2 {
		t.Fatalf("seed: n=%d err=%v", n, err)
	}
	got := do(t, e, "GET", "/widgets/w_1", "", nil)
	if got.status != 200 || bodyOf(t, got)["name"] != "gear" {
		t.Fatalf("a seeded id is the store key: %d %s", got.status, got.body)
	}
	list := do(t, e, "GET", "/widgets", "", nil)
	var page map[string]any
	json.Unmarshal([]byte(list.body), &page)
	if items, _ := page["items"].([]any); len(items) != 2 {
		t.Fatalf("both seeded resources list: %s", list.body)
	}
	if e.SeededCount() != 2 {
		t.Fatalf("seeded count: %d", e.SeededCount())
	}
	if _, err := e.Seed(map[string][]map[string]any{"widgets": {{"name": 5}}}); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("a seed item is validated like a create and the field is named: %v", err)
	}
	if _, err := e.Seed(map[string][]map[string]any{"gizmos": {{"name": "x"}}}); err == nil || !strings.Contains(err.Error(), "gizmos") {
		t.Fatalf("a type no operation declares is refused by name: %v", err)
	}
}

func TestSnapshotRestoreAndResetReturnToKnownState(t *testing.T) {
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_snap", Seed: "snap-1"})
	if _, err := e.Seed(map[string][]map[string]any{"widgets": {{"id": "w_1", "name": "gear"}}}); err != nil {
		t.Fatal(err)
	}
	token := e.Snapshot()
	if token == "" {
		t.Fatal("snapshot token")
	}
	if got := do(t, e, "POST", "/widgets", `{"name":"extra"}`, nil); got.status != 201 {
		t.Fatalf("create: %d", got.status)
	}
	if err := e.Restore(token); err != nil {
		t.Fatal(err)
	}
	if got := do(t, e, "GET", "/widgets/widgets_1", "", nil); got.status != 404 {
		t.Fatalf("restore drops what came after the snapshot: %d", got.status)
	}
	if got := do(t, e, "GET", "/widgets/w_1", "", nil); got.status != 200 {
		t.Fatalf("restore keeps what the snapshot held: %d", got.status)
	}
	if err := e.Restore("snap_nope"); err == nil || !strings.Contains(err.Error(), "snap_nope") {
		t.Fatalf("an unknown token is refused by name: %v", err)
	}

	do(t, e, "POST", "/widgets", `{"name":"again"}`, nil)
	e.ArmFault(FaultRule{Kind: "error", Status: 503, Method: "GET", Path: "/widgets", Probability: 1})
	if err := e.Reset(); err != nil {
		t.Fatal(err)
	}
	if len(e.Faults()) != 0 {
		t.Fatal("reset clears faults")
	}
	if entries, _ := e.JournalEntries(0); len(entries) != 0 {
		t.Fatal("reset clears the journal")
	}
	list := do(t, e, "GET", "/widgets", "", nil)
	var page map[string]any
	json.Unmarshal([]byte(list.body), &page)
	if items, _ := page["items"].([]any); len(items) != 1 {
		t.Fatalf("reset returns to the seeded state: %s", list.body)
	}
}
