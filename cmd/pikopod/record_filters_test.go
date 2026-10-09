package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/drift"
	"github.com/pikopod/pikopod/internal/scenario"
)

func TestReproduceNamesTheExcludeKeyWhenTheIncidentWasNeverRecorded(t *testing.T) {
	dir := cliDir(t)
	yaml := "listen: 127.0.0.1\ndata_dir: " + filepath.Join(dir, "data") + "\nupstreams:\n  widgets:\n    target: https://api.example.invalid\n    record:\n      exclude: [\"^/health\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "pikopod.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	writeFixEvent(t, dir, alert.DriftEvent{SchemaVersion: alert.SchemaVersion, Fingerprint: "fp_excluded0001", Upstream: "widgets", Method: "GET", Endpoint: "/health",
		StatusClass: "5xx", Kind: drift.UpstreamError, After: "503", Level: "ERR", FirstSeen: seen, LastSeen: seen, Occurrences: 1})
	_, err := runCLI(t, newReproduceCmd(), "fp_excluded0001")
	if err == nil || !strings.Contains(err.Error(), "record.exclude") || !strings.Contains(err.Error(), "never recorded") {
		t.Fatalf("the fix names the config key that kept the traffic off disk: %v", err)
	}
	_, err = runCLI(t, newIncidentsCmd(), "export", "fp_excluded0001")
	if err == nil || !strings.Contains(err.Error(), "record.exclude") {
		t.Fatalf("export says the same: %v", err)
	}
}

func TestAnInvalidRecordPatternIsAConfigurationError(t *testing.T) {
	dir := cliDir(t)
	yaml := "listen: 127.0.0.1\ndata_dir: " + filepath.Join(dir, "data") + "\nupstreams:\n  widgets:\n    target: https://api.example.invalid\n    record:\n      exclude: [\"^/health(\"]\n"
	os.WriteFile(filepath.Join(dir, "pikopod.yaml"), []byte(yaml), 0o600)
	_, err := runCLI(t, newSandboxCmd(), "list")
	if err == nil || !strings.Contains(err.Error(), "record.exclude") {
		t.Fatalf("a pattern that does not compile is refused at load: %v", err)
	}
}

func TestExplainPrintsACurlLineWithoutTheCredential(t *testing.T) {
	spec, _ := filepath.Abs("../../docs/demo/examplepay.spec.json")
	cliDir(t)
	if _, err := runCLI(t, newImportCmd(), "widgets", "--spec", spec, "--seed", "curl-1"); err != nil {
		t.Fatalf("import: %v", err)
	}
	listed, _ := runCLI(t, newSandboxCmd(), "list")
	credential := ""
	for _, line := range strings.Split(listed, "\n") {
		if i := strings.Index(line, "credential: "); i >= 0 {
			credential = strings.TrimSpace(line[i+len("credential: "):])
		}
	}
	if credential == "" {
		t.Fatalf("no credential in:\n%s", listed)
	}
	out, err := runCLI(t, newRequestsCmd(), "widgets", "--explain", "POST", "/charges", "--body", `{"amount":1500,"currency":"usd"}`)
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, out)
	}
	if strings.Contains(out, credential) {
		t.Fatalf("CREDENTIAL LEAK in explain output:\n%s", out)
	}
	for _, want := range []string{`curl -X POST "$PIKOPOD_URL/widgets/charges"`, `-H "authorization: Bearer $PIKOPOD_CREDENTIAL"`, `--data '{"amount":1500,"currency":"usd"}'`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestScenarioCheckPrintsACurlLineUnderAFailedRequestStep(t *testing.T) {
	spec, _ := filepath.Abs(widgetsSpecPath)
	dir := cliDir(t)
	if _, err := runCLI(t, newImportCmd(), "widgets", "--spec", spec, "--seed", "curl-2"); err != nil {
		t.Fatalf("import: %v", err)
	}
	pack := "name: wrong-status\nprovider: widgets\ndefinition:\n  steps:\n    - key: create\n      type: REQUEST\n      config: {method: POST, path: /widgets, headers: {Idempotency-Key: order-9}, body: {name: gear}}\n      assertions:\n        - {target: response.status, op: equals, expected: 503}\n"
	os.MkdirAll(filepath.Join(dir, "data", "scenarios"), 0o700)
	os.WriteFile(filepath.Join(dir, "data", "scenarios", "wrong-status.yaml"), []byte(pack), 0o600)
	cfg, err := loadConfig(newRootCmd())
	if err != nil {
		t.Fatal(err)
	}
	entry, def, err := loadSandboxDef(cfg, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := resolveRunnable(cfg, "wrong-status", def, nil)
	if err != nil {
		t.Fatal(err)
	}
	eng, done, err := scenarioEngineFrom(cfg, entry, def, false, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	res, err := scenario.RunWith(eng, parsed, nil, entry.Seed, scenario.RunOptions{Mount: "widgets"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writeRunResult(&buf, "wrong-status", res)
	out := buf.String()
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, `curl -X POST "$PIKOPOD_URL/widgets/widgets"`) || !strings.Contains(out, "idempotency-key: order-9") || !strings.Contains(out, `--data '{"name":"gear"}'`) {
		t.Fatalf("a FAILED request step carries the curl line to reproduce it:\n%s", out)
	}
	if strings.Contains(out, "pikopod_sbx_test_") {
		t.Fatalf("CREDENTIAL LEAK under a failed step:\n%s", out)
	}
}
