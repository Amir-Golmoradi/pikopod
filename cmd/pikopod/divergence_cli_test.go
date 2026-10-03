package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/drift"
)

func TestIncidentsListsDivergenceAsItsOwnClass(t *testing.T) {
	dir := cliDir(t)
	os.MkdirAll(filepath.Join(dir, "data"), 0o700)
	ev := alert.DriftEvent{SchemaVersion: alert.SchemaVersion, Fingerprint: "fp_0123456789ab", Upstream: "widgets", Method: "POST", Endpoint: "/widgets",
		StatusClass: "4xx", Kind: drift.BehaviourDivergence, Category: "undocumented_rule", Field: "body.name", Before: "201", After: "422", Level: "WARN",
		Detail: "sandbox answered 201, provider answered 422; request carried body.name", FirstSeen: time.Now(), LastSeen: time.Now(), Occurrences: 3}
	raw, _ := json.Marshal(ev)
	os.WriteFile(filepath.Join(dir, "data", "events.ndjson"), append(raw, '\n'), 0o600)

	out, _ := execRoot(t, "agent", "incidents", "--only", "divergence")
	for _, want := range []string{"divergence", "behaviour_divergence", "undocumented_rule", "POST /widgets", "reproduce: pikopod reproduce fp_0123456789ab"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	out, _ = execRoot(t, "agent", "incidents", "--only", "incidents")
	if strings.Contains(out, "fp_0123456789ab") {
		t.Fatalf("a divergence is not a failed exchange:\n%s", out)
	}
	if _, err := runCLI(t, newRootCmd(), "agent", "incidents", "--only", "shapes"); err == nil || !strings.Contains(err.Error(), "incidents, drift or divergence") {
		t.Fatalf("the fix names the three classes: %v", err)
	}
}
