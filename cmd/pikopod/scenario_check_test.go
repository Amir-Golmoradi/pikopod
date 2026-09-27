package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/config"
)

func TestScenarioCheckIsTheCommandAndRunIsAnAlias(t *testing.T) {
	spec, err := filepath.Abs(widgetsSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := cliDir(t)
	cfg, err := config.Load(filepath.Join(dir, "pikopod.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sandboxAdd(cfg, "widgets", spec, "check-seed", "", "", false, io.Discard); err != nil {
		t.Fatal(err)
	}

	help, err := runCLI(t, newScenarioCmd(), "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help, "\n  check ") {
		t.Fatalf("scenario --help must list check:\n%s", help)
	}
	if strings.Contains(help, "\n  run ") {
		t.Fatalf("scenario --help must not list run as a command:\n%s", help)
	}

	exec := func(verb string) (string, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		c := newScenarioCmd()
		c.SetOut(&out)
		c.SetErr(&errOut)
		c.SetArgs([]string{verb, "widgets", "declines"})
		if err := c.Execute(); err != nil {
			t.Fatalf("scenario %s: %v\n%s", verb, err, errOut.String())
		}
		return out.String(), errOut.String()
	}

	checkOut, checkErr := exec("check")
	runOut, runErr := exec("run")
	if checkOut != runOut {
		t.Fatalf("run and check must print identical stdout:\n--- check\n%s\n--- run\n%s", checkOut, runOut)
	}
	if !strings.Contains(checkOut, "declines — PASSED") {
		t.Fatalf("check should have run declines:\n%s", checkOut)
	}
	if checkErr != "" {
		t.Fatalf("check must not print to stderr, got %q", checkErr)
	}
	if lines := strings.Split(strings.TrimSpace(runErr), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "scenario check") {
		t.Fatalf("run must print exactly one line naming scenario check on stderr, got %q", runErr)
	}
}
