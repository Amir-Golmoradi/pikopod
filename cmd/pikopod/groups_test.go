package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/config"
)

func helpSections(help string) (map[string][]string, []string) {
	out := map[string][]string{}
	var order []string
	current := ""
	for _, line := range strings.Split(help, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			current = ""
		case !strings.HasPrefix(line, " "):
			current = strings.TrimSuffix(line, ":")
			order = append(order, current)
		case current != "" && strings.HasPrefix(line, "  "):
			out[current] = append(out[current], strings.Fields(line)[0])
		}
	}
	return out, order
}

func TestRootHelpHasTheSixSections(t *testing.T) {
	help, err := runCLI(t, newRootCmd(), "--help")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		title string
		names []string
	}{
		{"Sandbox", []string{"import", "up", "mode", "chaos", "webhook", "requests", "reproduce"}},
		{"Scenarios", []string{"scenario"}},
		{"CI", []string{"spec-diff"}},
		{"Observe", []string{"agent"}},
		{"Setup", []string{"init", "doctor", "demo", "mcp"}},
		{"More", []string{"fix", "pr", "sandbox"}},
	}
	got, seen := helpSections(help)
	var order []string
	for _, title := range seen {
		for _, w := range want {
			if title == w.title {
				order = append(order, title)
			}
		}
	}
	for i, w := range want {
		if i >= len(order) || order[i] != w.title {
			t.Fatalf("sections must appear in order %v, got %v\n%s", want, order, help)
		}
		if strings.Join(got[w.title], ",") != strings.Join(w.names, ",") {
			t.Errorf("%s lists %v, want %v\n%s", w.title, got[w.title], w.names, help)
		}
	}
	for _, old := range []string{"incidents", "status", "why", "replay", "ack", "accept"} {
		if strings.Contains(help, "\n  "+old+" ") {
			t.Errorf("old spelling %q must not be listed at the root:\n%s", old, help)
		}
	}
}

func TestAgentHelpListsEveryObserveCommand(t *testing.T) {
	help, err := runCLI(t, newRootCmd(), "agent", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"incidents", "status", "report", "inspect", "ack", "accept", "baseline", "conformance", "contract", "spec-update", "volatile", "replay"} {
		if !strings.Contains(help, "\n  "+name+" ") {
			t.Errorf("agent --help must list %s:\n%s", name, help)
		}
	}
	sub, err := runCLI(t, newRootCmd(), "agent", "incidents", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sub, "Old spelling: `pikopod incidents`") {
		t.Fatalf("the new command's help must name its old spelling:\n%s", sub)
	}
}

func execRoot(t *testing.T, args ...string) (string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := newRootCmd()
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)
	if err := c.Execute(); err != nil {
		t.Fatalf("pikopod %s: %v\n%s", strings.Join(args, " "), err, errOut.String())
	}
	return out.String(), errOut.String()
}

func TestOldSpellingsStillWorkAndSayWhereTheyWent(t *testing.T) {
	spec, err := filepath.Abs(widgetsSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := cliDir(t)
	cfg, err := config.Load(filepath.Join(dir, "pikopod.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sandboxAdd(cfg, "widgets", spec, "groups-seed", "", "", false, io.Discard); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		old, now []string
		notice   string
	}{
		{[]string{"why", "widgets", "GET", "/widgets"}, []string{"requests", "widgets", "--explain", "GET", "/widgets"}, "`pikopod why` is now `pikopod requests <sandbox> --explain <METHOD> <path>`"},
		{[]string{"incidents"}, []string{"agent", "incidents"}, "`pikopod incidents` is now `pikopod agent incidents`"},
	}
	for _, tc := range cases {
		newOut, newErr := execRoot(t, tc.now...)
		oldOut, oldErr := execRoot(t, tc.old...)
		if newErr != "" {
			t.Errorf("%v must not print to stderr, got %q", tc.now, newErr)
		}
		if oldOut != newOut {
			t.Errorf("%v and %v must print identical stdout:\n--- new\n%s\n--- old\n%s", tc.old, tc.now, newOut, oldOut)
		}
		lines := strings.Split(strings.TrimSpace(oldErr), "\n")
		if len(lines) != 1 || !strings.HasPrefix(lines[0], tc.notice) {
			t.Errorf("%v must print exactly one notice line starting %q, got %q", tc.old, tc.notice, oldErr)
		}
	}
}
