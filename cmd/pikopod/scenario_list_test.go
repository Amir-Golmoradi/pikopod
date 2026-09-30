package main

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/config"
)

func setupTestSandbox(t *testing.T) *config.Config {
	t.Helper()
	absSpec, err := filepath.Abs(widgetsSpecPath)
	if err != nil {
		t.Fatalf("absSpec: %v", err)
	}
	dir := cliDir(t)
	cfg, err := config.Load(filepath.Join(dir, "pikopod.yaml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := sandboxAdd(cfg, "widgets", absSpec, "test-seed-1", "", "", false, io.Discard); err != nil {
		t.Fatalf("sandboxAdd: %v", err)
	}
	return cfg
}

func TestScenarioListDefaultOutput(t *testing.T) {
	cfg := setupTestSandbox(t)

	var out bytes.Buffer
	if err := scenarioList(cfg, "widgets", false, &out); err != nil {
		t.Fatalf("scenarioList: %v", err)
	}

	got := out.String()

	if !strings.Contains(got, "archetypes vs widgets") {
		t.Fatalf("expected header 'archetypes vs widgets', got:\n%s", got)
	}
	if !strings.Contains(got, "✓ happy_path") {
		t.Fatalf("expected '✓ happy_path' in output, got:\n%s", got)
	}

	if strings.Contains(got, "      op=") {
		t.Fatalf("non-verbose output must not contain candidate bindings, got:\n%s", got)
	}
}

func TestScenarioListVerboseOutput(t *testing.T) {
	cfg := setupTestSandbox(t)

	var out bytes.Buffer
	if err := scenarioList(cfg, "widgets", true, &out); err != nil {
		t.Fatalf("scenarioList verbose: %v", err)
	}

	got := out.String()

	if !strings.Contains(got, "  ✓ happy_path                 Happy path  (1 candidate binding(s))\n      op=ep_1a284091a72f\n") {
		t.Fatalf("expected exact happy_path binding in verbose output, got:\n%s", got)
	}
	if !strings.Contains(got, "  ✓ rate_limit_backoff         Rate limit and backoff  (6 candidate binding(s))\n      op=ep_12d71306d3d7\n") {
		t.Fatalf("expected exact rate_limit_backoff binding in verbose output, got:\n%s", got)
	}
}

func TestScenarioListCLIFlag(t *testing.T) {
	setupTestSandbox(t)

	cmdNonVerbose := newScenarioCmd()
	outNonVerbose, err := runCLI(t, cmdNonVerbose, "list", "widgets")
	if err != nil {
		t.Fatalf("CLI scenario list failed: %v", err)
	}
	if strings.Contains(outNonVerbose, "      op=") {
		t.Fatalf("CLI non-verbose should not have '      op=', got:\n%s", outNonVerbose)
	}

	cmdVerbose := newScenarioCmd()
	outVerbose, err := runCLI(t, cmdVerbose, "list", "widgets", "--verbose")
	if err != nil {
		t.Fatalf("CLI scenario list --verbose failed: %v", err)
	}
	if !strings.Contains(outVerbose, "      op=") {
		t.Fatalf("CLI --verbose should have '      op=', got:\n%s", outVerbose)
	}

	cmdShortVerbose := newScenarioCmd()
	outShortVerbose, err := runCLI(t, cmdShortVerbose, "list", "widgets", "-v")
	if err != nil {
		t.Fatalf("CLI scenario list -v failed: %v", err)
	}
	if !strings.Contains(outShortVerbose, "      op=") {
		t.Fatalf("CLI -v should have '      op=', got:\n%s", outShortVerbose)
	}

	if outVerbose != outShortVerbose {
		t.Fatalf("--verbose and -v output must match exactly.\n--verbose:\n%s\n-v:\n%s", outVerbose, outShortVerbose)
	}
}

func TestScenarioListFormatJSONMatchesMCP(t *testing.T) {
	cfg := setupTestSandbox(t)

	out, err := runCLI(t, newScenarioCmd(), "list", "widgets", "--format", "json")
	if err != nil {
		t.Fatalf("scenario list --format json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}

	res, _ := callTool(t, cfg, "scenario_list", map[string]any{"sandbox": "widgets"})
	want, _ := res["data"].(map[string]any)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CLI JSON and MCP payload differ.\ncli: %v\nmcp: %v", got, want)
	}

	arch, _ := got["archetypes"].([]any)
	if len(arch) == 0 {
		t.Fatal("archetypes missing")
	}
	sawReason := false
	for _, a := range arch {
		m := a.(map[string]any)
		if _, ok := m["applicable"]; !ok {
			t.Fatalf("archetype without applicable: %v", m)
		}
		if m["applicable"] == false && m["reason"] != "" {
			sawReason = true
		}
	}
	if !sawReason {
		t.Fatal("a non-binding archetype must carry its reason")
	}
}

func TestScenarioListRejectsUnknownFormat(t *testing.T) {
	setupTestSandbox(t)
	if _, err := runCLI(t, newScenarioCmd(), "list", "widgets", "--format", "yaml"); err == nil {
		t.Fatal("an unknown --format must be an error")
	}
}

func TestScenarioListBadFormatIsReportedBeforeMissingConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCLI(t, newScenarioCmd(), "list", "x", "--format", "yaml")
	if err == nil || !strings.Contains(err.Error(), "unknown --format") {
		t.Fatalf("a bad --format must be reported before config loading, got: %v", err)
	}
}
