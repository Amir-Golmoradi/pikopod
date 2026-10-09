package curlline

import (
	"strings"
	"testing"
)

func TestTheLineCarriesThePlaceholdersNeverTheCredential(t *testing.T) {
	headers := map[string]string{
		"authorization":   "Bearer sk_test_RAWCANARY",
		"content-type":    "application/json",
		"idempotency-key": "order-77",
		"user-agent":      "curl/8",
		"content-length":  "27",
		"x-pikopod-scope": "worker-1",
	}
	line := Line("POST", "examplepay", "/charges?expand=1", headers, []byte(`{"amount":5000,"note":"it's"}`), "sk_test_RAWCANARY")
	if strings.Contains(line, "RAWCANARY") {
		t.Fatalf("CANARY LEAK: %s", line)
	}
	for _, want := range []string{`curl -X POST "$PIKOPOD_URL/examplepay/charges?expand=1"`, `-H "authorization: Bearer $PIKOPOD_CREDENTIAL"`, `-H 'content-type: application/json'`, `-H 'idempotency-key: order-77'`, `--data '{"amount":5000,"note":"it'\''s"}'`} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %s", want, line)
		}
	}
	for _, drop := range []string{"user-agent", "content-length", "x-pikopod-scope"} {
		if strings.Contains(line, drop) {
			t.Errorf("%s is not part of a reproduction: %s", drop, line)
		}
	}
	if Line("GET", "", "/health", nil, nil, "") != `curl -X GET "$PIKOPOD_URL/health"` {
		t.Fatalf("a bare read: %s", Line("GET", "", "/health", nil, nil, ""))
	}
	placeholder := Line("GET", "pay", "/x", map[string]string{"x-api-key": "[REDACTED]"}, nil, "")
	if !strings.Contains(placeholder, `-H "x-api-key: $PIKOPOD_CREDENTIAL"`) || strings.Contains(placeholder, "REDACTED") {
		t.Fatalf("a redacted credential header renders as the placeholder: %s", placeholder)
	}
}
