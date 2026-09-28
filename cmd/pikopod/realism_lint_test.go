package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/config"
)

func TestImportAndListPrintTheRealismLint(t *testing.T) {
	spec, err := filepath.Abs(widgetsSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := cliDir(t)
	cfg, err := config.Load(filepath.Join(dir, "pikopod.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := sandboxAdd(cfg, "widgets", spec, "lint-seed", "", "", false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "synthesised without spec, example or convention") {
		t.Fatalf("import must print the realism lint:\n%s", out.String())
	}
	listed, _ := execRoot(t, "sandbox", "list")
	if !strings.Contains(listed, "synthesised without spec, example or convention") {
		t.Fatalf("sandbox list must print the realism lint:\n%s", listed)
	}
	_ = io.Discard
}
