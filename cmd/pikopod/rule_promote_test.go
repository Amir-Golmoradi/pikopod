package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/drift"
	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/sandbox"
)

func promoteDir(t *testing.T) string {
	t.Helper()
	dir, _ := truthDir(t)
	ts := time.Now().Add(-time.Minute)
	writeCLIRecordings(t, dir, []proxy.Record{
		{TS: ts, Upstream: "widgets", Method: "POST", Path: "/widgets", Status: 201, ReqKind: "json", ReqBody: map[string]any{"name": "gear_7f3a9c", "size": float64(3)},
			Redacted: []proxy.SectionRedaction{{Section: "req_body", Pointer: "/name", Mode: "TOKENIZE"}},
			RespKind: "json", RespBody: map[string]any{"id": "wdg_1", "name": "gear_7f3a9c", "size": float64(3)}},
		{TS: ts.Add(time.Second), Upstream: "widgets", Method: "POST", Path: "/widgets", Status: 422, ReqKind: "json", ReqBody: map[string]any{"name": "gear_7f3a9c", "size": float64(3)},
			Redacted: []proxy.SectionRedaction{{Section: "req_body", Pointer: "/name", Mode: "TOKENIZE"}},
			RespKind: "json", RespBody: map[string]any{"code": "duplicate_name"}},
	})
	events := []alert.DriftEvent{
		{SchemaVersion: alert.SchemaVersion, Fingerprint: "fp_0123456789ab", Upstream: "widgets", Method: "POST", Endpoint: "/widgets", StatusClass: "4xx",
			Kind: drift.BehaviourDivergence, Category: "undocumented_rule", Field: "body.name,body.size", Before: "201", After: "422", Level: "WARN",
			Detail: "sandbox answered 201, provider answered 422; request carried body.name, body.size", FirstSeen: ts, LastSeen: ts, Occurrences: 1},
		{SchemaVersion: alert.SchemaVersion, Fingerprint: "fp_fedcba987654", Upstream: "widgets", Method: "POST", Endpoint: "/widgets", StatusClass: "5xx",
			Kind: drift.UpstreamError, After: "503", Level: "ERR", FirstSeen: ts, LastSeen: ts, Occurrences: 1},
	}
	f, err := os.Create(filepath.Join(dir, "data", "events.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, ev := range events {
		enc.Encode(ev)
	}
	f.Close()
	return dir
}

func TestRulePromoteWritesTheRuleFromTheDivergence(t *testing.T) {
	dir := promoteDir(t)
	out, err := runCLI(t, newRuleCmd(), "promote", "fp_0123456789ab", "--yes")
	if err != nil {
		t.Fatalf("promote: %v\n%s", err, out)
	}
	rs, err := sandbox.LoadRuleSet(sandbox.RulesPath(filepath.Join(dir, "data"), "widgets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Rules) != 1 {
		t.Fatalf("one rule written: %+v", rs.Rules)
	}
	r := rs.Rules[0]
	if r.Provenance != "promoted:fp_0123456789ab" || r.When.Method != "POST" || r.When.Path != "/widgets" || r.Respond.Status != 422 {
		t.Fatalf("rule: %+v", r)
	}
	if string(r.When.Body["name"]) != `"exists_in_store"` {
		t.Fatalf("a tokenized identifier becomes exists_in_store: %s", r.When.Body["name"])
	}
	var size bytes.Buffer
	json.Compact(&size, r.When.Body["size"])
	if size.String() != `{"equals":3}` {
		t.Fatalf("a readable value becomes equals: %s", r.When.Body["size"])
	}
	if !strings.Contains(string(r.Respond.Body), "duplicate_name") {
		t.Fatalf("the recorded body is the response: %s", r.Respond.Body)
	}
	for _, want := range []string{"undocumented_rule", "POST /widgets", "422", "exists_in_store", "before: 201", "after: 422", "promoted-0123456789ab"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gear_7f3a9c") == false {
		t.Logf("tokens may be shown; raw values never exist here")
	}
}

func TestRulePromoteAsksTwiceAndWritesNothingOnNo(t *testing.T) {
	dir := promoteDir(t)
	c := newRuleCmd()
	c.SetIn(strings.NewReader("y\nn\n"))
	out, err := runCLI(t, c, "promote", "fp_0123456789ab")
	if err != nil {
		t.Fatalf("promote: %v\n%s", err, out)
	}
	if !strings.Contains(out, "provider's behaviour") || !strings.Contains(out, "every caller") || !strings.Contains(out, "nothing written") {
		t.Fatalf("both questions, then nothing written:\n%s", out)
	}
	if _, err := os.Stat(sandbox.RulesPath(filepath.Join(dir, "data"), "widgets")); !os.IsNotExist(err) {
		rs, _ := sandbox.LoadRuleSet(sandbox.RulesPath(filepath.Join(dir, "data"), "widgets"))
		if rs != nil && len(rs.Rules) != 0 {
			t.Fatalf("no rule may be written after a no: %+v", rs.Rules)
		}
	}
}

func TestRulePromoteRefusesANonDivergence(t *testing.T) {
	promoteDir(t)
	_, err := runCLI(t, newRuleCmd(), "promote", "fp_fedcba987654", "--yes")
	if err == nil || !strings.Contains(err.Error(), "reproduce") {
		t.Fatalf("an incident is reproduced, not promoted: %v", err)
	}
}
