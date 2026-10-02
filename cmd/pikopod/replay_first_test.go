package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/proxy"
)

func registryRecordings(t *testing.T, dir, name string) string {
	t.Helper()
	entries, err := loadRegistry(dir + "/data")
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(entries, name)
	if e == nil {
		t.Fatalf("sandbox %s not registered", name)
	}
	return e.Recordings
}

func TestImportChoosesTheRecordingsModeFromWhatExists(t *testing.T) {
	spec, err := filepath.Abs(widgetsSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	bare := cliDir(t)
	if _, err := runCLI(t, newImportCmd(), "widgets", "--spec", spec, "--seed", "rf-cli-1"); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := registryRecordings(t, bare, "widgets"); got != "off" {
		t.Fatalf("with nothing recorded the sandbox synthesises only: %q", got)
	}
	out, err := runCLI(t, newSandboxCmd(), "list")
	if err != nil || !strings.Contains(out, "recordings=off") {
		t.Fatalf("sandbox list must show the mode: %v\n%s", err, out)
	}

	recorded := cliDir(t)
	writeCLIRecordings(t, recorded, []proxy.Record{cliRecord("GET", "/widgets/w_1", 200, map[string]any{"id": "w_1", "name": "gear"})})
	if _, err := runCLI(t, newImportCmd(), "widgets", "--spec", spec, "--seed", "rf-cli-2"); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := registryRecordings(t, recorded, "widgets"); got != "first" {
		t.Fatalf("with recordings on disk they answer first: %q", got)
	}
	out, err = runCLI(t, newSandboxCmd(), "list")
	if err != nil || !strings.Contains(out, "recordings=first") {
		t.Fatalf("sandbox list must show the mode: %v\n%s", err, out)
	}

	if _, err := runCLI(t, newImportCmd(), "widgets", "--spec", spec, "--update", "--recordings", "off"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := registryRecordings(t, recorded, "widgets"); got != "off" {
		t.Fatalf("--update honours the flag: %q", got)
	}
	if _, err := runCLI(t, newImportCmd(), "widgets", "--spec", spec, "--update"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := registryRecordings(t, recorded, "widgets"); got != "first" {
		t.Fatalf("--update without the flag re-evaluates the default: %q", got)
	}

	explicit := cliDir(t)
	if _, err := runCLI(t, newImportCmd(), "widgets", "--spec", spec, "--recordings", "fallback"); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := registryRecordings(t, explicit, "widgets"); got != "fallback" {
		t.Fatalf("the flag wins over the default: %q", got)
	}
	if _, err := runCLI(t, newImportCmd(), "other", "--spec", spec, "--recordings", "sometimes"); err == nil || !strings.Contains(err.Error(), "first, fallback or off") {
		t.Fatalf("an unknown mode is refused by name: %v", err)
	}
}
