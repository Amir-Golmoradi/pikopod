package e2e

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModeVerifyEndToEnd(t *testing.T) {
	dir := t.TempDir()
	ports := freePorts(t, 2)
	agentPort, sbxPort := ports[0], ports[1]
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), []byte(thingsSpec), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("listen: 127.0.0.1\nagent_port: %d\nsandbox_port: %d\ndata_dir: data\nupstreams:\n  prov:\n    target: https://api.example.invalid\n", agentPort, sbxPort)
	if err := os.WriteFile(filepath.Join(dir, "pikopod.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := run(t, dir, "import", "prov", "--spec", "spec.json", "--config", "pikopod.yaml"); code != 0 {
		t.Fatalf("import failed (%d): %s", code, out)
	}
	up := startUp(t, dir, agentPort)
	defer up.stop(t)

	out, code := run(t, dir, "mode", "verify", "prov", "--config", "pikopod.yaml")
	if code != 2 || !strings.Contains(out, "no mode set") {
		t.Fatalf("verify with no mode must exit 2 and say so, got %d:\n%s", code, out)
	}

	out, code = run(t, dir, "mode", "set", "prov", "retry_storm", "--config", "pikopod.yaml")
	if code != 0 {
		t.Fatalf("mode set failed (%d): %s", code, out)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	post := func() int {
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/prov/things", sbxPort), strings.NewReader(`{"amount": 5}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "order-77")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	post()
	post()

	out, code = run(t, dir, "mode", "verify", "prov", "--config", "pikopod.yaml")
	if code != 1 || !strings.Contains(out, "FAILED") {
		t.Fatalf("two attempts must verify as FAILED with exit 1, got %d:\n%s", code, out)
	}

	if status := post(); status != 201 {
		t.Fatalf("third attempt under the same key must recover with 201, got %d", status)
	}
	out, code = run(t, dir, "mode", "verify", "prov", "--config", "pikopod.yaml")
	if code != 0 || !strings.Contains(out, "PASSED") || !strings.Contains(out, "storm-shape") {
		t.Fatalf("three attempts must verify as PASSED with exit 0 and name the step, got %d:\n%s", code, out)
	}
}
