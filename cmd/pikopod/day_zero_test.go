package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedFile(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "seed.yaml")
	if err := os.WriteFile(p, []byte("widgets:\n  - id: w_1\n    name: gear\n  - id: w_2\n    name: cog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func adminPost(t *testing.T, url string, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestImportSeedDataAndSandboxListShowIt(t *testing.T) {
	spec, _ := filepath.Abs(widgetsSpecPath)
	dir := cliDir(t)
	if _, err := runCLI(t, newImportCmd(), "widgets", "--spec", spec, "--seed", "dz-1", "--seed-data", seedFile(t, dir)); err != nil {
		t.Fatalf("import: %v", err)
	}
	out, err := runCLI(t, newSandboxCmd(), "list")
	if err != nil || !strings.Contains(out, "seeded: 2 resources") {
		t.Fatalf("sandbox list must show the seed: %v\n%s", err, out)
	}
	cfg, err := loadConfig(newRootCmd())
	if err != nil {
		t.Fatal(err)
	}
	sbx, err := newSandboxServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sbx.Close()
	srv := httptest.NewServer(sbx)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/widgets/widgets/w_2")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("a seeded resource answers after a restart: %d", resp.StatusCode)
	}
}

func TestControlPlaneSeedSnapshotRestoreReset(t *testing.T) {
	cfg := testConfig(t, "https://example.invalid")
	if err := sandboxAdd(cfg, "widgets", widgetsSpecPath, "dz-2", "", "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	sbx, err := newSandboxServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sbx.Close()
	srv := httptest.NewServer(sbx)
	defer srv.Close()
	base := srv.URL + "/_pikopod/sandboxes/widgets"

	if code, out := adminPost(t, base+"/seed", `{"widgets":[{"id":"w_1","name":"gear"}]}`); code != 200 || out["seeded"] != float64(1) {
		t.Fatalf("seed: %d %v", code, out)
	}
	code, snap := adminPost(t, base+"/snapshot", "")
	token, _ := snap["token"].(string)
	if code != 200 || token == "" {
		t.Fatalf("snapshot: %d %v", code, snap)
	}
	if resp, _ := http.Post(srv.URL+"/widgets/widgets", "application/json", strings.NewReader(`{"name":"extra"}`)); resp.StatusCode != 201 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	if code, _ := adminPost(t, base+"/restore", `{"token":"`+token+`"}`); code != 200 {
		t.Fatalf("restore: %d", code)
	}
	if resp, _ := http.Get(srv.URL + "/widgets/widgets/widgets_1"); resp.StatusCode != 404 {
		t.Fatalf("restore drops the later create: %d", resp.StatusCode)
	}
	if code, out := adminPost(t, base+"/restore", `{"token":"snap_nope"}`); code != 404 || !strings.Contains(out["message"].(string), "snap_nope") {
		t.Fatalf("unknown token: %d %v", code, out)
	}

	http.Post(srv.URL+"/widgets/widgets", "application/json", strings.NewReader(`{"name":"again"}`))
	adminPost(t, base+"/faults", `{"kind":"error","status":503,"method":"GET","path":"/widgets"}`)
	adminPost(t, base+"/mode", `{"name":"retry_storm"}`)
	if code, out := adminPost(t, base+"/reset", ""); code != 200 || out["reset"] != true || out["seeded"] != float64(1) {
		t.Fatalf("reset: %d %v", code, out)
	}
	resp, _ := http.Get(base + "/faults")
	var faults map[string]any
	json.NewDecoder(resp.Body).Decode(&faults)
	resp.Body.Close()
	if list, _ := faults["faults"].([]any); len(list) != 0 {
		t.Fatalf("reset clears faults: %v", faults)
	}
	resp, _ = http.Get(base + "/mode")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), "retry_storm") {
		t.Fatalf("reset clears the standing mode: %s", raw)
	}
	resp, _ = http.Get(srv.URL + "/widgets/widgets")
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(raw), "gear") || strings.Contains(string(raw), "again") {
		t.Fatalf("reset returns to the seeded state: %s", raw)
	}
}

func TestControlPlaneForksAreIsolatedDeterministicAndReaped(t *testing.T) {
	forkIdleTimeout, forkReapEvery = 300*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { forkIdleTimeout, forkReapEvery = 10*time.Minute, time.Minute })
	cfg := testConfig(t, "https://example.invalid")
	if err := sandboxAdd(cfg, "widgets", widgetsSpecPath, "dz-3", "", "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	sbx, err := newSandboxServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sbx.Close()
	srv := httptest.NewServer(sbx)
	defer srv.Close()
	base := srv.URL + "/_pikopod/sandboxes/widgets"
	adminPost(t, base+"/seed", `{"widgets":[{"id":"w_1","name":"gear"}]}`)

	var forks []string
	for i := 0; i < 2; i++ {
		code, out := adminPost(t, base+"/fork", "")
		name, _ := out["name"].(string)
		if code != 201 || !strings.HasPrefix(name, "widgets--") || out["credential"] == nil {
			t.Fatalf("fork: %d %v", code, out)
		}
		forks = append(forks, name)
	}
	var bodies []string
	for _, f := range forks {
		if resp, _ := http.Get(srv.URL + "/" + f + "/widgets/w_1"); resp.StatusCode != 200 {
			t.Fatalf("a fork starts from the seeded state: %d", resp.StatusCode)
		}
		resp, _ := http.Post(srv.URL+"/"+f+"/widgets", "application/json", strings.NewReader(`{"name":"mine"}`))
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodies = append(bodies, string(raw))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("two forks with one seed answer the same bytes:\n%s\n%s", bodies[0], bodies[1])
	}
	if resp, _ := http.Get(srv.URL + "/widgets/widgets/widgets_1"); resp.StatusCode != 404 {
		t.Fatalf("a create in a fork never reaches the parent: %d", resp.StatusCode)
	}
	if resp, _ := http.Get(base + "/forks"); resp.StatusCode != 200 {
		t.Fatalf("forks list: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/_pikopod/sandboxes/"+forks[0]+"/fork", nil)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 200 {
		t.Fatalf("delete fork: %d", resp.StatusCode)
	}
	if resp, _ := http.Get(srv.URL + "/" + forks[0] + "/widgets"); resp.StatusCode != 404 {
		t.Fatalf("a deleted fork is gone: %d", resp.StatusCode)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, _ := http.Get(base + "/forks")
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(raw), forks[1]) {
			if resp, _ := http.Get(srv.URL + "/" + forks[1] + "/widgets"); resp.StatusCode != 404 {
				t.Fatalf("a reaped fork is gone: %d", resp.StatusCode)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("an idle fork is reaped")
}

func TestUpSpecServesASandboxWithoutAConfigFile(t *testing.T) {
	spec, _ := filepath.Abs(widgetsSpecPath)
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	done := make(chan error, 1)
	ready := make(chan string, 1)
	go func() {
		done <- upSpec(ctx, upSpecOptions{Spec: spec, Name: "widgets", Seed: "dz-4", Port: 0, Started: func(url string) { ready <- url }}, &out)
	}()
	var url string
	select {
	case url = <-ready:
	case err := <-done:
		t.Fatalf("up --spec exited early: %v\n%s", err, out.String())
	case <-time.After(10 * time.Second):
		t.Fatal("up --spec never started")
	}
	resp, err := http.Get(url + "/widgets/widgets")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("the sandbox serves without pikopod.yaml: %d", resp.StatusCode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("clean exit: %v", err)
	}
	text := out.String()
	for _, want := range []string{"pikopod sandbox on", "widgets", "credential", "no agent"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if _, err := os.Stat("pikopod.yaml"); !os.IsNotExist(err) {
		t.Fatal("up --spec must not write a config file")
	}
}
