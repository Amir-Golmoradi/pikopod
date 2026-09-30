package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postWidget(t *testing.T, srv *httptest.Server, idempotencyKey string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/widgets/widgets", strings.NewReader(`{"name":"g"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func verifyMode(t *testing.T, srv *httptest.Server) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(srv.URL+"/_pikopod/sandboxes/widgets/mode/verify", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, readJSON(t, res)
}

func TestModeVerifyProvesWhatTheClientSent(t *testing.T) {
	srv := modeServer(t, "verify-seed-1")
	if res := setMode(t, srv, `{"name":"retry_storm"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("mode set = %d: %v", res.StatusCode, readJSON(t, res)["message"])
	}
	got := []int{postWidget(t, srv, "k-1"), postWidget(t, srv, "k-1"), postWidget(t, srv, "k-1")}
	if got[0] != 503 || got[1] != 503 || got[2] != 201 {
		t.Fatalf("retry_storm must answer 503, 503, 201 for one key, got %v", got)
	}

	code, body := verifyMode(t, srv)
	if code != http.StatusOK {
		t.Fatalf("verify = %d: %v", code, body["message"])
	}
	result, _ := body["result"].(map[string]any)
	if result["status"] != "PASSED" {
		t.Fatalf("three retries under one key must verify as PASSED, got %v", body)
	}
	if result["summary"] != "3 matcher(s) matched in order" {
		t.Fatalf("verify summary must count matchers, got %v", result["summary"])
	}
	steps, _ := result["steps"].([]any)
	if len(steps) == 0 {
		t.Fatalf("verify must report the verification steps it ran: %v", body)
	}
}

func TestModeVerifyFailsWhenTheClientDidNotRetry(t *testing.T) {
	srv := modeServer(t, "verify-seed-2")
	if res := setMode(t, srv, `{"name":"retry_storm"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("mode set = %d", res.StatusCode)
	}
	postWidget(t, srv, "k-1")
	postWidget(t, srv, "k-1")

	code, body := verifyMode(t, srv)
	if code != http.StatusOK {
		t.Fatalf("verify = %d: %v", code, body["message"])
	}
	result, _ := body["result"].(map[string]any)
	if result["status"] != "FAILED" {
		t.Fatalf("two attempts is not a retry storm survived; want FAILED, got %v", body)
	}
	if summary, _ := result["summary"].(string); !strings.Contains(summary, "never matched") {
		t.Fatalf("the failure must say which matcher never matched, got %q", summary)
	}
}

func TestModeVerifyRefusesWithoutAMode(t *testing.T) {
	srv := modeServer(t, "verify-seed-3")
	code, body := verifyMode(t, srv)
	if code != http.StatusConflict {
		t.Fatalf("verify without a mode = %d, want 409: %v", code, body)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "no mode set") {
		t.Fatalf("refusal must say no mode is set, got %q", msg)
	}
}

func TestModeVerifyDoesNotArmOrSeedTheServedSandbox(t *testing.T) {
	srv := modeServer(t, "verify-seed-4")
	if res := setMode(t, srv, `{"name":"retry_storm"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("mode set = %d", res.StatusCode)
	}
	postWidget(t, srv, "k-1")
	postWidget(t, srv, "k-1")
	postWidget(t, srv, "k-1")
	verifyMode(t, srv)

	res, err := http.Get(srv.URL + "/_pikopod/sandboxes/widgets/faults")
	if err != nil {
		t.Fatal(err)
	}
	faults, _ := readJSON(t, res)["faults"].([]any)
	if len(faults) != 1 {
		t.Fatalf("verify must leave the mode's single armed fault untouched, got %d faults", len(faults))
	}
}
