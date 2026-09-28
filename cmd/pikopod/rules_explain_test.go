package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/sandbox"
)

func TestExplainNamesTheRuleThatFired(t *testing.T) {
	spec, err := filepath.Abs(widgetsSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := cliDir(t)
	cfg, err := config.Load(filepath.Join(dir, "pikopod.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sandboxAdd(cfg, "widgets", spec, "rules-seed", "", "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	rules := `{"version":1,"rules":[{"id":"named-only","version":1,"provenance":"manual",
	  "when":{"method":"POST","path":"/widgets","body":{"name":"absent"}},
	  "respond":{"status":422,"body":{"error":"name is required"}}}]}`
	path := sandbox.RulesPath(cfg.DataDir, "widgets")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _ := execRoot(t, "requests", "widgets", "--explain", "POST", "/widgets", "--body", `{"size":3}`)
	if !strings.Contains(out, "rule named-only fired: POST /widgets") || !strings.Contains(out, "→ 422") {
		t.Fatalf("explain must name the rule and show its answer:\n%s", out)
	}
	out, _ = execRoot(t, "requests", "widgets", "--explain", "POST", "/widgets", "--body", `{"name":"g"}`)
	if strings.Contains(out, "rule named-only fired") || !strings.Contains(out, "→ 201") {
		t.Fatalf("a rule that does not match must not be named as fired:\n%s", out)
	}
}
