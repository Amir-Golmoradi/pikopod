package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/drift"
	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/sandbox"
)

const importCanary = "RAWVALUE_IMPORTCANARY_9f2e"

func exportDir(t *testing.T) string {
	t.Helper()
	dir := cliDir(t)
	seen := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	ev := alert.DriftEvent{SchemaVersion: alert.SchemaVersion, Fingerprint: "fp_import000001", Upstream: "widgets", Method: "GET", Endpoint: "/widgets/w_{id}",
		StatusClass: "5xx", Kind: drift.UpstreamError, After: "503", Level: "ERR", FirstSeen: seen, LastSeen: seen, Occurrences: 2}
	writeFixEvent(t, dir, ev)
	writeCLIRecordings(t, dir, []proxy.Record{
		{TS: seen.Add(-time.Hour), Upstream: "widgets", Method: "GET", Path: "/widgets/w_8c1f2a", Status: 200, RespKind: "json",
			RespBody: map[string]any{"id": "w_8c1f2a", "name": "gear", "status": "pending"},
			Redacted: []proxy.SectionRedaction{{Section: "resp_body", Pointer: "/id", Mode: "TOKENIZE"}}},
		{TS: seen, Upstream: "widgets", Method: "GET", Path: "/widgets/w_8c1f2a", Status: 503, RespKind: "json", RespBody: map[string]any{"message": "down"}},
	})
	os.WriteFile(filepath.Join(dir, "data", ".salt"), []byte(importCanary), 0o600)
	return dir
}

func importDir(t *testing.T, spec string) (string, *config.Config) {
	t.Helper()
	dir := cliDir(t)
	cfg, err := config.Load(filepath.Join(dir, "pikopod.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sandboxAdd(cfg, "widgets", spec, "import-seed", "", "widgets", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	return dir, cfg
}

func TestExportCapturesTheResourceStateItLastSaw(t *testing.T) {
	exportDir(t)
	out, err := runCLI(t, newIncidentsCmd(), "export", "fp_import000001")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var b struct {
		State *struct {
			Type       string         `json:"type"`
			Key        string         `json:"key"`
			Attributes map[string]any `json:"attributes"`
			State      *string        `json:"state"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	if b.State == nil || b.State.Type != "/widgets" || b.State.Key != "w_8c1f2a" || b.State.Attributes["name"] != "gear" || b.State.State == nil || *b.State.State != "pending" {
		t.Fatalf("the state section carries the resource the recording touched: %+v\n%s", b.State, out)
	}
	if strings.Contains(out, importCanary) {
		t.Fatal("CANARY LEAK in the bundle")
	}
}

func TestImportInstallsRuleSeedsResourceAndReproduces(t *testing.T) {
	spec, _ := filepath.Abs(widgetsSpecPath)
	src := exportDir(t)
	bundleOut, err := runCLI(t, newIncidentsCmd(), "export", "fp_import000001")
	if err != nil {
		t.Fatal(err)
	}
	_ = src
	dir, cfg := importDir(t, spec)
	bundle := filepath.Join(dir, "incident.json")
	os.WriteFile(bundle, []byte(bundleOut), 0o600)

	out, err := runCLI(t, newIncidentsCmd(), "import", bundle)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	for _, want := range []string{"event fp_import000001", "rule imported-import000001", "seeded /widgets", "reproduce: pikopod reproduce fp_import000001"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	rs, err := sandbox.LoadRuleSet(sandbox.RulesPath(cfg.DataDir, "widgets"))
	if err != nil || len(rs.Rules) != 1 {
		t.Fatalf("one imported rule: %v %+v", err, rs)
	}
	r := rs.Rules[0]
	if r.Provenance != "imported:fp_import000001" || r.Respond.Status != 503 || r.When.Method != "GET" || r.When.Path != "/widgets/{id}" {
		t.Fatalf("rule: %+v", r)
	}
	if !strings.Contains(string(r.When.State["/widgets"]), "pending") {
		t.Fatalf("the rule is conditioned on the imported state: %s", r.When.State["/widgets"])
	}
	entries, _ := loadRegistry(cfg.DataDir)
	st, err := sandbox.OpenStore(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.GetOne(entries[0].ID, "/widgets", "w_8c1f2a")
	st.Close()
	if err != nil || res.State == nil || *res.State != "pending" {
		t.Fatalf("the resource is seeded into the local store in its last state: %v %+v", err, res)
	}
	evs, _ := loadEvents(cfg.DataDir)
	if len(evs) != 1 || evs[0].Fingerprint != "fp_import000001" {
		t.Fatalf("the event is installed locally: %+v", evs)
	}

	rep, err := runCLI(t, newReproduceCmd(), "fp_import000001")
	if err != nil || !strings.Contains(rep, "PASSED") {
		t.Fatalf("reproduce lands on the seeded resource: %v\n%s", err, rep)
	}
	explain, err := runCLI(t, newRequestsCmd(), "widgets", "--explain", "GET", "/widgets/w_8c1f2a")
	if err != nil || !strings.Contains(explain, "imported-import000001") {
		t.Fatalf("explain shows the imported rule: %v\n%s", err, explain)
	}

	again, err := runCLI(t, newIncidentsCmd(), "import", bundle)
	if err != nil || !strings.Contains(again, "already") {
		t.Fatalf("importing twice is a no-op: %v\n%s", err, again)
	}
	rs, _ = sandbox.LoadRuleSet(sandbox.RulesPath(cfg.DataDir, "widgets"))
	evs, _ = loadEvents(cfg.DataDir)
	if len(rs.Rules) != 1 || len(evs) != 1 {
		t.Fatalf("no duplicates after a second import: %d rules, %d events", len(rs.Rules), len(evs))
	}

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(strings.Replace(bundleOut, `"schema_version": 1`, `"schema_version": 1, "salt": "x"`, 1)), 0o600)
	if _, err := runCLI(t, newIncidentsCmd(), "import", bad); err == nil || !strings.Contains(err.Error(), "does not know") {
		t.Fatalf("a bundle with an unknown field is refused: %v", err)
	}
	filepath.Walk(filepath.Join(dir, "data"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if raw, _ := os.ReadFile(path); strings.Contains(string(raw), importCanary) {
				t.Fatalf("CANARY LEAK in %s", path)
			}
		}
		return nil
	})
}
