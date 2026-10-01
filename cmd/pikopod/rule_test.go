package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/sandbox"
)

func ruleCLIDir(t *testing.T) (string, *config.Config) {
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
	if err := sandboxAdd(cfg, "widgets", spec, "rule-seed", "", "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	return dir, cfg
}

func writeRuleFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const uniqueNameRule = "id: unique-name\nwhen:\n  method: POST\n  path: /widgets\n  body:\n    name: exists_in_store\nrespond:\n  status: 422\n  body:\n    error: name already taken\n"

func TestRuleAddListDrop(t *testing.T) {
	dir, cfg := ruleCLIDir(t)
	file := writeRuleFile(t, dir, "unique.yaml", uniqueNameRule)

	out, _ := execRoot(t, "rule", "add", "widgets", file)
	if !strings.Contains(out, "added unique-name") || !strings.Contains(out, "version 1") {
		t.Fatalf("add must name the rule and the set version:\n%s", out)
	}
	rs, err := sandbox.LoadRuleSet(sandbox.RulesPath(cfg.DataDir, "widgets"))
	if err != nil || len(rs.Rules) != 1 || rs.Rules[0].ID != "unique-name" || rs.Rules[0].Version != 1 || rs.Rules[0].Provenance != "manual" {
		t.Fatalf("the rule must be saved beside the IR: %+v %v", rs, err)
	}

	listed, _ := execRoot(t, "rule", "list", "widgets")
	for _, want := range []string{"unique-name", "POST /widgets when body.name exists_in_store", "422 body", "manual", "1"} {
		if !strings.Contains(listed, want) {
			t.Errorf("list missing %q:\n%s", want, listed)
		}
	}

	dropped, _ := execRoot(t, "rule", "drop", "widgets", "unique-name")
	if !strings.Contains(dropped, "dropped unique-name") {
		t.Fatalf("drop must confirm by name:\n%s", dropped)
	}
	rs, _ = sandbox.LoadRuleSet(sandbox.RulesPath(cfg.DataDir, "widgets"))
	if len(rs.Rules) != 0 || rs.Version != 2 {
		t.Fatalf("drop must save the set and advance its version: %+v", rs)
	}
	empty, _ := execRoot(t, "rule", "list", "widgets")
	if !strings.Contains(empty, "no rules") {
		t.Fatalf("an empty set says so:\n%s", empty)
	}
}

func TestRuleAddRefusesByName(t *testing.T) {
	dir, cfg := ruleCLIDir(t)
	ghost := writeRuleFile(t, dir, "ghost.yaml", "id: ghost\nwhen:\n  method: POST\n  path: /nope\nrespond:\n  status: 500\n")
	c := newRootCmd()
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"rule", "add", "widgets", ghost})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "does not declare") {
		t.Fatalf("a rule on an undeclared route is refused by name: %v", err)
	}
	if _, statErr := os.Stat(sandbox.RulesPath(cfg.DataDir, "widgets")); statErr == nil {
		t.Fatal("a refused add must not write the rules file")
	}

	execRoot(t, "rule", "add", "widgets", writeRuleFile(t, dir, "unique.yaml", uniqueNameRule))
	c = newRootCmd()
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"rule", "add", "widgets", writeRuleFile(t, dir, "dup.yaml", uniqueNameRule)})
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "unique-name") {
		t.Fatalf("a duplicate id is refused by name: %v", err)
	}

	c = newRootCmd()
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"rule", "drop", "widgets", "missing"})
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("dropping an unknown id is refused by name: %v", err)
	}
}

func TestRuleCheckReportsAndExits(t *testing.T) {
	dir, cfg := ruleCLIDir(t)
	execRoot(t, "rule", "add", "widgets", writeRuleFile(t, dir, "always.yaml", "id: always-503\nwhen:\n  method: POST\n  path: /widgets\nrespond:\n  status: 503\n"))
	execRoot(t, "rule", "add", "widgets", writeRuleFile(t, dir, "unique.yaml", uniqueNameRule))

	var out bytes.Buffer
	findings, err := ruleCheck(cfg, "widgets", &out)
	if err != nil {
		t.Fatal(err)
	}
	if findings != 2 {
		t.Fatalf("two findings expected, got %d:\n%s", findings, out.String())
	}
	text := out.String()
	for _, want := range []string{"always-503", "shadows", "every POST /widgets", "unique-name", "never fires"} {
		if !strings.Contains(text, want) {
			t.Errorf("check output missing %q:\n%s", want, text)
		}
	}

	execRoot(t, "rule", "drop", "widgets", "always-503")
	out.Reset()
	if findings, err := ruleCheck(cfg, "widgets", &out); err != nil || findings != 0 || !strings.Contains(out.String(), "no problems") {
		t.Fatalf("a clean set says so: %d %v\n%s", findings, err, out.String())
	}
}

func TestRulesReachARunningSandboxAtOnce(t *testing.T) {
	dir, cfg := ruleCLIDir(t)
	sbx, err := newSandboxServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sbx.Close()
	srv := httptest.NewServer(sbx)
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	cfg.SandboxPort = port

	post := func() int {
		resp, err := http.Post(srv.URL+"/widgets/widgets", "application/json", strings.NewReader(`{"name":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if post() != 201 {
		t.Fatal("no rules yet")
	}

	rs, _ := sandbox.ParseRuleFile([]byte("id: teapot\nwhen:\n  method: POST\n  path: /widgets\nrespond:\n  status: 418\n"))
	applied, err := pushRules(cfg, "widgets", &sandbox.RuleSet{Version: 1, Rules: rs})
	if err != nil || !applied {
		t.Fatalf("a running sandbox must take the new set: applied=%v err=%v", applied, err)
	}
	if post() != 418 {
		t.Fatal("the served sandbox must answer by the new rule at once")
	}

	resp, err := http.Get(srv.URL + "/_pikopod/sandboxes/widgets/rules")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"teapot"`) {
		t.Fatalf("GET rules must return the live set: %d %s", resp.StatusCode, raw)
	}

	cfg.SandboxPort = 1
	applied, err = pushRules(cfg, "widgets", &sandbox.RuleSet{})
	if err != nil || applied {
		t.Fatalf("with no server the push is skipped, not an error: applied=%v err=%v", applied, err)
	}
	_ = dir
}
