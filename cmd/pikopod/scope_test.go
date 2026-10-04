package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func postWidgetScoped(t *testing.T, srv *httptest.Server, idempotencyKey, scope string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/widgets/widgets", strings.NewReader(`{"name":"g"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	if scope != "" {
		req.Header.Set("X-Pikopod-Scope", scope)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func verifyScoped(t *testing.T, srv *httptest.Server, scope string) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(srv.URL+"/_pikopod/v1/sandboxes/widgets/mode/verify?scope="+scope, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, readJSON(t, res)
}

func journalScoped(t *testing.T, srv *httptest.Server, scope string) (string, map[string]any) {
	t.Helper()
	url := srv.URL + "/_pikopod/v1/sandboxes/widgets/requests"
	if scope != "" {
		url += "?scope=" + scope
	}
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var body map[string]any
	json.Unmarshal(raw, &body)
	return string(raw), body
}

func TestScopedClientsRunRetryStormConcurrentlyAgainstOneSandbox(t *testing.T) {
	srv := modeServer(t, "scope-seed-1")
	if res := setMode(t, srv, `{"name":"retry_storm"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("mode set = %d", res.StatusCode)
	}
	scopes := []string{"worker-1-CANARYSCOPE", "worker-2-CANARYSCOPE"}
	got := make([][]int, len(scopes))
	var wg sync.WaitGroup
	for i, scope := range scopes {
		wg.Add(1)
		go func(i int, scope string) {
			defer wg.Done()
			for range 3 {
				got[i] = append(got[i], postWidgetScoped(t, srv, "k-shared", scope))
			}
		}(i, scope)
	}
	wg.Wait()
	for i, statuses := range got {
		if fmt.Sprint(statuses) != "[503 503 201]" {
			t.Fatalf("scope %s must see its own window: 503, 503, 201; got %v", scopes[i], statuses)
		}
	}
	for _, scope := range scopes {
		code, body := verifyScoped(t, srv, scope)
		result, _ := body["result"].(map[string]any)
		if code != http.StatusOK || result["status"] != "PASSED" {
			t.Fatalf("verify --scope %s = %d %v", scope, code, body)
		}
	}
	code, body := verifyScoped(t, srv, "")
	result, _ := body["result"].(map[string]any)
	if code != http.StatusOK || result["status"] != "FAILED" {
		t.Fatalf("the unscoped partition saw no request, so the unscoped verify must fail: %d %v", code, body)
	}
	raw, body := journalScoped(t, srv, "")
	if strings.Contains(raw, "CANARYSCOPE") || strings.Contains(raw, "x-pikopod-scope") {
		t.Fatalf("the scope header must be stripped before journaling:\n%s", raw)
	}
	if entries, _ := body["requests"].([]any); len(entries) != 6 {
		t.Fatalf("the unfiltered journal shows every partition: %d entries", len(entries))
	}
	_, body = journalScoped(t, srv, scopes[0])
	if entries, _ := body["requests"].([]any); len(entries) != 3 {
		t.Fatalf("requests?scope= shows one partition: %d entries", len(entries))
	}
}

func TestUnscopedClientIsUnaffectedByScopedWindows(t *testing.T) {
	srv := modeServer(t, "scope-seed-2")
	if res := setMode(t, srv, `{"name":"retry_storm"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("mode set = %d", res.StatusCode)
	}
	postWidgetScoped(t, srv, "k-1", "scoped-suite")
	postWidgetScoped(t, srv, "k-1", "scoped-suite")
	if got := postWidgetScoped(t, srv, "k-1", ""); got != 503 {
		t.Fatalf("the unscoped partition has its own window, so the first unscoped attempt is refused; got %d", got)
	}
	if got := postWidgetScoped(t, srv, "k-1", "scoped-suite"); got != 201 {
		t.Fatalf("the scoped window was not consumed by the unscoped request; got %d", got)
	}
}

func TestScopesAreBoundedAndEvictionIsCounted(t *testing.T) {
	srv := modeServer(t, "scope-seed-3")
	for i := range 257 {
		postWidgetScoped(t, srv, "k", fmt.Sprintf("s-%d", i))
	}
	_, body := journalScoped(t, srv, "")
	scopes, _ := body["scopes"].(map[string]any)
	if scopes["live"] != float64(256) || scopes["evicted"] != float64(1) {
		t.Fatalf("256 live scopes and one eviction: %v", body["scopes"])
	}
	_, oldest := journalScoped(t, srv, "s-0")
	if entries, _ := oldest["requests"].([]any); len(entries) != 0 {
		t.Fatalf("the oldest idle scope was evicted, its journal is gone: %d entries", len(entries))
	}
	_, next := journalScoped(t, srv, "s-1")
	if entries, _ := next["requests"].([]any); len(entries) != 1 {
		t.Fatalf("the next scope survives: %d entries", len(entries))
	}
	code, verify := verifyScoped(t, srv, "s-0")
	if code != http.StatusConflict {
		t.Fatalf("no mode is set, verify must still refuse first: %d %v", code, verify)
	}
}

func TestAnOverlongScopeIsRefused(t *testing.T) {
	srv := modeServer(t, "scope-seed-4")
	if got := postWidgetScoped(t, srv, "k", strings.Repeat("x", 129)); got != http.StatusBadRequest {
		t.Fatalf("a scope longer than 128 characters is refused, not truncated: %d", got)
	}
}
