package main

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

	"github.com/pikopod/pikopod/internal/sandbox"
)

type cpExample struct {
	title, method, path, body string
	want                      int
	status                    int
	response                  string
}

func (ex *cpExample) run(t *testing.T, srv *httptest.Server) {
	t.Helper()
	var body io.Reader
	if ex.body != "" {
		body = strings.NewReader(ex.body)
	}
	req, _ := http.NewRequest(ex.method, srv.URL+ex.path, body)
	if ex.body != "" {
		req.Header.Set("content-type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ex.status = resp.StatusCode
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		ex.response = pretty.String()
	} else {
		ex.response = strings.TrimSpace(string(raw))
	}
	if ex.status != ex.want {
		t.Fatalf("%s %s: got %d want %d: %s", ex.method, ex.path, ex.status, ex.want, raw)
	}
}

func TestControlPlaneV1Examples(t *testing.T) {
	spec, _ := filepath.Abs("../../docs/demo/examplepay.spec.json")
	cfg := testConfig(t, "https://example.invalid")
	if err := sandboxAdd(cfg, "examplepay", spec, "docs", "", "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	sbx, err := newSandboxServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sbx.Close()
	srv := httptest.NewServer(sbx)
	defer srv.Close()
	cred := sandbox.IssuedCredential("docs")
	v1 := "/_pikopod/v1/sandboxes/examplepay"

	charge := func(key string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/examplepay/charges", strings.NewReader(`{"amount":5000,"currency":"usd"}`))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+cred)
		req.Header.Set("idempotency-key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	examples := []*cpExample{
		{title: "Rules: read the live set", method: "GET", path: v1 + "/rules", want: 200},
		{title: "Rules: replace the live set", method: "PUT", path: v1 + "/rules", want: 200,
			body: `{"version":1,"rules":[{"id":"unique-reference","provenance":"manual","when":{"method":"POST","path":"/charges","body":{"reference":"exists_in_store"}},"respond":{"status":422,"body":{"error":"reference already used"}}}]}`},
		{title: "Faults: arm one", method: "POST", path: v1 + "/faults", want: 201, body: `{"kind":"error","status":503,"method":"POST","path":"/charges","times":2,"per":"idempotency-key"}`},
		{title: "Faults: list standing faults", method: "GET", path: v1 + "/faults", want: 200},
		{title: "Faults: clear matching faults", method: "DELETE", path: v1 + "/faults?method=POST&path=/charges", want: 200},
		{title: "Mode: enter a scenario's standing state", method: "POST", path: v1 + "/mode", want: 201, body: `{"name":"retry_storm"}`},
	}
	for _, ex := range examples {
		ex.run(t, srv)
	}
	if got := []int{charge("order-77"), charge("order-77"), charge("order-77")}; got[0] != 503 || got[1] != 503 || got[2] != 201 {
		t.Fatalf("retry_storm answers 503, 503, 201: %v", got)
	}
	more := []*cpExample{
		{title: "Mode: verify what the client sent", method: "POST", path: v1 + "/mode/verify", want: 200},
		{title: "Mode: show the standing mode", method: "GET", path: v1 + "/mode", want: 200},
		{title: "Mode: clear it", method: "DELETE", path: v1 + "/mode", want: 200},
		{title: "Requests: the journal", method: "GET", path: v1 + "/requests?limit=2", want: 200},
		{title: "Requests: reset the journal", method: "DELETE", path: v1 + "/requests", want: 200},
		{title: "Webhooks: deliveries so far", method: "GET", path: v1 + "/webhooks", want: 200},
		{title: "Webhooks: emit an event the spec does not declare", method: "POST", path: v1 + "/webhooks/emit", want: 400, body: `{"event":"charge.succeeded"}`},
		{title: "Seed: store resources", method: "POST", path: v1 + "/seed", want: 200, body: `{"charges":[{"id":"ch_8f3a91","amount":5000,"currency":"usd","status":"success"}]}`},
		{title: "Snapshot: mark a point", method: "POST", path: v1 + "/snapshot", want: 200},
		{title: "Restore: return to it", method: "POST", path: v1 + "/restore", want: 200, body: `{"token":"snap_1"}`},
		{title: "Reset: back to the seeded state", method: "POST", path: v1 + "/reset", want: 200},
		{title: "Fork: a fresh copy for one test", method: "POST", path: v1 + "/fork", want: 201},
		{title: "Forks: the live copies", method: "GET", path: v1 + "/forks", want: 200},
	}
	for _, ex := range more {
		ex.run(t, srv)
	}
	var forked struct {
		Name string `json:"name"`
	}
	json.Unmarshal([]byte(more[len(more)-2].response), &forked)
	last := &cpExample{title: "Fork: delete it", method: "DELETE", path: "/_pikopod/v1/sandboxes/" + forked.Name + "/fork", want: 200}
	last.run(t, srv)
	all := append(append(examples, more...), last)

	unversioned := &cpExample{method: "GET", path: "/_pikopod/sandboxes/examplepay/faults", want: 200}
	unversioned.run(t, srv)

	out := os.Getenv("PIKOPOD_CONTROL_PLANE_EXAMPLES")
	if out == "" {
		return
	}
	var b strings.Builder
	b.WriteString("{/* generated by TestControlPlaneV1Examples in cmd/pikopod; do not edit */}\n\n")
	b.WriteString("The sandbox was imported from the example spec with seed `docs`; the client traffic between the mode and its verification was three `POST /charges` under one idempotency key.\n\n")
	for _, ex := range all {
		fmt.Fprintf(&b, "#### %s\n\n```text\n%s %s\n```\n\n", ex.title, ex.method, ex.path)
		if ex.body != "" {
			var pretty bytes.Buffer
			json.Indent(&pretty, []byte(ex.body), "", "  ")
			fmt.Fprintf(&b, "```json\n%s\n```\n\n", pretty.String())
		}
		fmt.Fprintf(&b, "`%d`\n\n```json\n%s\n```\n\n", ex.status, ex.response)
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
