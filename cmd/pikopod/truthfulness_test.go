package main

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/proxy"
)

func truthDir(t *testing.T) (string, *config.Config) {
	t.Helper()
	spec, err := filepath.Abs(widgetsSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := cliDir(t)
	cfg, err := config.Load(filepath.Join(dir, "pikopod.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sandboxAdd(cfg, "widgets", spec, "truth-seed", "", "widgets", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	return dir, cfg
}

func TestAgentTruthfulnessNeedsRecordings(t *testing.T) {
	truthDir(t)
	c := newRootCmd()
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"agent", "truthfulness", "widgets"})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "no recordings") || !strings.Contains(err.Error(), "run your tests once") {
		t.Fatalf("with no recordings the command refuses and says how to get some: %v", err)
	}
}

func TestAgentTruthfulnessPrintsTheNumber(t *testing.T) {
	dir, _ := truthDir(t)
	writeCLIRecordings(t, dir, []proxy.Record{
		{TS: cliRecord("POST", "/widgets", 201, nil).TS, Upstream: "widgets", Method: "POST", Path: "/widgets", Status: 201, ReqKind: "json", ReqBody: map[string]any{"name": "gear", "size": float64(3)}, RespKind: "json", RespBody: map[string]any{"id": "w_9", "name": "gear", "size": float64(3)}},
		cliRecord("GET", "/widgets/w_9", 200, map[string]any{"id": "w_9", "name": "gear", "size": float64(3)}),
	})
	out, _ := execRoot(t, "agent", "truthfulness", "widgets")
	for _, want := range []string{"POST /widgets", "GET /widgets/{id}", "truthfulness:", "over 2 recorded responses", "←"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	var buf bytes.Buffer
	c := newRootCmd()
	c.SetOut(&buf)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"agent", "truthfulness", "widgets", "--format", "json"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, buf.String())
	}
	if report["responses"] != float64(2) || report["percent"] == nil || report["perEndpoint"] == nil || report["worst"] == nil {
		t.Fatalf("json report: %v", report)
	}
}
