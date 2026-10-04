package sandboxtest_test

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/pkg/sandboxtest"
)

func examplepay(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../docs/demo/examplepay.spec.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func createCharge(client *http.Client, url string, attempts int) int {
	status := 0
	for i := 0; i < attempts; i++ {
		req, _ := http.NewRequest("POST", url+"/charges", bytes.NewReader([]byte(`{"amount":5000,"currency":"usd"}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "order-77")
		resp, err := client.Do(req)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		status = resp.StatusCode
		if status < 500 {
			return status
		}
	}
	return status
}

func TestClientWithRetriesSurvivesARetryStorm(t *testing.T) {
	s := sandboxtest.New(t, examplepay(t))
	s.Mode("retry_storm")
	if got := createCharge(s.Client(), s.URL(), 5); got != 201 {
		t.Fatalf("the third attempt succeeds: %d", got)
	}
	res := s.Verify()
	if !res.Passed() {
		t.Fatalf("a client that retries passes: %s", res.Summary)
	}
	if len(s.Requests()) != 3 {
		t.Fatalf("three requests were journaled: %d", len(s.Requests()))
	}
}

func TestClientWithoutRetriesFailsVerify(t *testing.T) {
	s := sandboxtest.New(t, examplepay(t), sandboxtest.WithSeed("fixed"))
	s.Mode("retry_storm")
	if got := createCharge(s.Client(), s.URL(), 1); got != 503 {
		t.Fatalf("the first answer is the storm: %d", got)
	}
	res := s.Verify()
	if res.Passed() || !strings.Contains(res.Summary, "never matched") {
		t.Fatalf("a client that gives up fails, and the summary says what never happened: %+v", res)
	}
}

func TestSeedDataChaosAndReset(t *testing.T) {
	s := sandboxtest.New(t, examplepay(t), sandboxtest.WithSeedData(map[string][]map[string]any{
		"charges": {{"id": "ch_1", "amount": 100, "currency": "usd", "status": "success"}},
	}))
	resp, err := s.Client().Get(s.URL() + "/charges/ch_1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("seeded resource answers: %d", resp.StatusCode)
	}
	s.Chaos(sandboxtest.Fault{Kind: "error", Status: 503, Method: "GET", Path: "/charges/{id}"})
	if resp, _ := s.Client().Get(s.URL() + "/charges/ch_1"); resp.StatusCode != 503 {
		t.Fatalf("armed fault fires: %d", resp.StatusCode)
	}
	s.Reset()
	if len(s.Requests()) != 0 {
		t.Fatal("reset clears the journal")
	}
	if resp, _ := s.Client().Get(s.URL() + "/charges/ch_1"); resp.StatusCode != 200 {
		t.Fatalf("reset clears faults and keeps the seed: %d", resp.StatusCode)
	}
}
