package sandbox

import (
	"encoding/json"
	"testing"

	"github.com/pikopod/pikopod/internal/importer"
	"github.com/pikopod/pikopod/internal/ir"
)

const declinesSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Charges", "version": "1.0.0"},
  "paths": {
    "/charges": {
      "post": {
        "requestBody": {"content": {"application/json": {"schema": {"type": "object", "properties": {"amount": {"type": "integer"}}}}}},
        "responses": {
          "201": {"description": "created", "content": {"application/json": {"schema": {"type": "object", "properties": {"id": {"type": "string"}, "amount": {"type": "integer"}}}}}},
          "402": {"description": "declined", "content": {"application/json": {
            "schema": {"type": "object", "properties": {"error": {"type": "object", "properties": {"code": {"type": "string"}, "decline_code": {"type": "string"}}}}},
            "example": {"error": {"code": "card_declined", "decline_code": "insufficient_funds"}}
          }}}
        }
      }
    }
  }
}`

func loadDeclines(t *testing.T) *ir.ApiDefinition {
	t.Helper()
	def, err := importer.NormalizeOpenAPI([]byte(declinesSpec))
	if err != nil {
		t.Fatalf("normalize declines spec: %v", err)
	}
	return def
}

func sameJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is not JSON: %q (%v)", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %q (%v)", want, err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("body mismatch:\n got %s\nwant %s", gb, wb)
	}
}

func TestArmedErrorAnswersWithItsBody(t *testing.T) {
	e := newEngine(t, loadWidgets(t), Config{ID: "sbx_fb", Seed: "fb-1"})
	e.ArmFault(FaultRule{Method: "POST", Path: "/widgets", Kind: "error", Status: 402, Probability: 1,
		Body:    json.RawMessage(`{"error":{"code":"card_declined"}}`),
		Headers: map[string]string{"x-request-id": "req_fb_1"}})

	got := do(t, e, "POST", "/widgets", `{"name":"g"}`, nil)
	if got.status != 402 {
		t.Fatalf("status = %d, want 402", got.status)
	}
	sameJSON(t, got.body, `{"error":{"code":"card_declined"}}`)
	if ct := got.headers["content-type"]; ct != jsonContentType {
		t.Fatalf("content-type = %q, want %q", ct, jsonContentType)
	}
	if got.headers["x-request-id"] != "req_fb_1" {
		t.Fatalf("armed header did not reach the wire: %v", got.headers)
	}
	if got.headers[FaultAppliedHeader] != "error" {
		t.Fatalf("%s = %q, want error", FaultAppliedHeader, got.headers[FaultAppliedHeader])
	}
}

func TestArmedErrorUsesTheDeclaredExampleForItsStatus(t *testing.T) {
	e := newEngine(t, loadDeclines(t), Config{ID: "sbx_fx", Seed: "fx-1"})
	e.ArmFault(FaultRule{Method: "POST", Path: "/charges", Kind: "error", Status: 402, Probability: 1})

	got := do(t, e, "POST", "/charges", `{"amount":5}`, nil)
	if got.status != 402 {
		t.Fatalf("status = %d, want 402", got.status)
	}
	sameJSON(t, got.body, `{"error":{"code":"card_declined","decline_code":"insufficient_funds"}}`)
	if ct := got.headers["content-type"]; ct != jsonContentType {
		t.Fatalf("content-type = %q, want %q", ct, jsonContentType)
	}

	explicit := newEngine(t, loadDeclines(t), Config{ID: "sbx_fx2", Seed: "fx-1"})
	explicit.ArmFault(FaultRule{Method: "POST", Path: "/charges", Kind: "error", Status: 402, Probability: 1,
		Body: json.RawMessage(`{"error":{"code":"expired_card"}}`)})
	sameJSON(t, do(t, explicit, "POST", "/charges", `{"amount":5}`, nil).body, `{"error":{"code":"expired_card"}}`)
}

func TestArmedErrorWithoutASourceIsUnchanged(t *testing.T) {
	e := newEngine(t, loadDeclines(t), Config{ID: "sbx_fn", Seed: "fn-1"})
	e.ArmFault(FaultRule{Method: "POST", Path: "/charges", Kind: "error", Status: 503, Probability: 1})

	got := do(t, e, "POST", "/charges", `{"amount":5}`, nil)
	if got.status != 503 {
		t.Fatalf("status = %d, want 503", got.status)
	}
	if got.body != "" {
		t.Fatalf("a 503 the spec never described must stay bodiless, got %q", got.body)
	}
	if _, has := got.headers["content-type"]; has {
		t.Fatalf("no body means no content type, got %v", got.headers)
	}
	if got.headers[FaultAppliedHeader] != "error" {
		t.Fatalf("%s = %q, want error", FaultAppliedHeader, got.headers[FaultAppliedHeader])
	}
}
