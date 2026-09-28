package sandbox

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/importer"
	"github.com/pikopod/pikopod/internal/ir"
)

const ordersSpec = `{
  "openapi": "3.1.0",
  "info": {"title": "Orders", "version": "1.0.0"},
  "paths": {
    "/orders": {
      "post": {
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/OrderInput"}}}},
        "responses": {
          "201": {"description": "Order created", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Order"}}}},
          "404": {"description": "Order not found", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Failure"}}}}
        }
      }
    },
    "/orders/{orderId}": {
      "parameters": [{"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}}],
      "get": {"responses": {
        "200": {"description": "Order found", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Order"}}}},
        "404": {"description": "Order not found", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Failure"}}}}
      }},
      "put": {
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/OrderInput"}}}},
        "responses": {"200": {"description": "Order replaced", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Order"}}}}}
      }
    }
  },
  "components": {"schemas": {
    "OrderInput": {"type": "object", "properties": {
      "reference": {"type": "string"}, "amount": {"type": "integer"}, "currency": {"type": "string"}, "customer_email": {"type": "string"}
    }},
    "Order": {"type": "object", "properties": {
      "id": {"type": "string"},
      "status": {"type": "boolean"},
      "message": {"type": "string"},
      "reference": {"type": "string"},
      "amount": {"type": "integer"},
      "currency": {"type": "string"},
      "customer_email": {"type": "string"},
      "created_at": {"type": "string"},
      "updatedAt": {"type": "string"},
      "receipt_url": {"type": "string"},
      "domain": {"type": "string"},
      "order_code": {"type": "string"},
      "access_token": {"type": "string"},
      "kind": {"type": "string", "example": "standard"},
      "region": {"type": "string", "default": "eu"},
      "success": {"type": "boolean", "example": false},
      "active": {"type": "boolean", "default": true},
      "note": {"type": "string"}
    }},
    "Failure": {"type": "object", "properties": {"status": {"type": "boolean"}, "message": {"type": "string"}, "code": {"type": "string"}}}
  }}
}`

func loadOrders(t *testing.T) *ir.ApiDefinition {
	t.Helper()
	def, err := importer.NormalizeOpenAPI([]byte(ordersSpec))
	if err != nil {
		t.Fatalf("normalize orders spec: %v", err)
	}
	return def
}

func ordersEngine(t *testing.T, seed string) *Engine {
	t.Helper()
	e := newEngine(t, loadOrders(t), Config{ID: "sbx_orders", Seed: seed})
	e.SetMountPrefix("/orders")
	return e
}

func bodyOf(t *testing.T, got recorded) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(got.body), &m); err != nil {
		t.Fatalf("body is not a JSON object: %q (%v)", got.body, err)
	}
	return m
}

func isISO(s string) bool {
	_, err := time.Parse("2006-01-02T15:04:05.000Z", s)
	return err == nil
}

func TestConventionsOnASuccessfulCreate(t *testing.T) {
	e := ordersEngine(t, "conv-1")
	got := do(t, e, "POST", "/orders", `{"reference":"order-77","amount":5000,"currency":"ngn","customer_email":"a@b.test"}`, nil)
	if got.status != 201 {
		t.Fatalf("create: %d %s", got.status, got.body)
	}
	b := bodyOf(t, got)
	checks := []struct {
		field string
		ok    bool
	}{
		{"status", b["status"] == true},
		{"message", b["message"] == "Order created"},
		{"reference", b["reference"] == "order-77"},
		{"amount", b["amount"] == float64(5000)},
		{"currency", b["currency"] == "ngn"},
		{"customer_email", b["customer_email"] == "a@b.test"},
		{"created_at", isISO(asString(b["created_at"]))},
		{"updatedAt", isISO(asString(b["updatedAt"])) && b["updatedAt"] == b["created_at"]},
		{"receipt_url", strings.HasPrefix(asString(b["receipt_url"]), "https://orders.test/")},
		{"domain", b["domain"] == "test"},
		{"order_code", strings.HasPrefix(asString(b["order_code"]), "ord_") && len(asString(b["order_code"])) > 8},
		{"access_token", strings.HasPrefix(asString(b["access_token"]), "acc_")},
		{"kind", b["kind"] == "standard"},
		{"region", b["region"] == "eu"},
		{"success", b["success"] == false},
		{"active", b["active"] == true},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s = %v", c.field, b[c.field])
		}
	}
	again := bodyOf(t, do(t, ordersEngine(t, "conv-1"), "POST", "/orders", `{"reference":"order-77","amount":5000,"currency":"ngn","customer_email":"a@b.test"}`, nil))
	if again["note"] != b["note"] || again["order_code"] != b["order_code"] {
		t.Fatalf("the same seed must synthesise the same values: %v vs %v", again, b)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func TestConventionsWithoutRequestValues(t *testing.T) {
	b := bodyOf(t, do(t, ordersEngine(t, "conv-2"), "POST", "/orders", `{}`, nil))
	if !strings.HasPrefix(asString(b["reference"]), "ref_") {
		t.Errorf("reference = %v, want a ref_ token", b["reference"])
	}
	if amount, ok := b["amount"].(float64); !ok || amount < 100 || amount > 100000 {
		t.Errorf("amount = %v, want minor units between 100 and 100000", b["amount"])
	}
	if b["currency"] != "USD" {
		t.Errorf("currency = %v, want USD", b["currency"])
	}
	if strings.Contains(asString(b["order_code"]), " ") || len(asString(b["order_code"])) < 8 {
		t.Errorf("order_code = %v", b["order_code"])
	}
}

func TestConventionsOnAnError(t *testing.T) {
	got := do(t, ordersEngine(t, "conv-3"), "GET", "/orders/nope", "", nil)
	if got.status != 404 {
		t.Fatalf("missing order: %d", got.status)
	}
	b := bodyOf(t, got)
	if b["status"] != false || b["message"] != "Order not found" {
		t.Fatalf("error body must say so by convention: %v", b)
	}
	if !strings.HasPrefix(asString(b["code"]), "code_") {
		t.Errorf("code = %v, want a code_ token", b["code"])
	}
}

func TestUpdatedAtNeverBeforeCreatedAt(t *testing.T) {
	e := ordersEngine(t, "conv-4")
	created := bodyOf(t, do(t, e, "POST", "/orders", `{"reference":"r1"}`, nil))
	e.SetVirtualClockMs(e.VirtualClockMs() + 90000)
	replaced := bodyOf(t, do(t, e, "PUT", "/orders/orders_1", `{"reference":"r1","amount":1}`, nil))
	if replaced["created_at"] != created["created_at"] {
		t.Fatalf("created_at must survive a replace: %v then %v", created["created_at"], replaced["created_at"])
	}
	c, _ := time.Parse("2006-01-02T15:04:05.000Z", asString(replaced["created_at"]))
	u, _ := time.Parse("2006-01-02T15:04:05.000Z", asString(replaced["updatedAt"]))
	if !u.After(c) {
		t.Fatalf("updatedAt %v must be after created_at %v", u, c)
	}
}

func TestSynthTraceNamesEachSource(t *testing.T) {
	e := ordersEngine(t, "conv-5")
	var lines []string
	e.SetTrace(func(stage, message string) {
		if stage == "synth" {
			lines = append(lines, message)
		}
	})
	do(t, e, "POST", "/orders", `{"reference":"r1"}`, nil)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"order_code ← convention ord_", "note ← fallback", "status ← convention true on 201", "kind ← example"} {
		if !strings.Contains(joined, want) {
			t.Errorf("trace missing %q:\n%s", want, joined)
		}
	}
}

func TestRequiredIsEnforcedOnlyWhenDeclared(t *testing.T) {
	strict := strings.Replace(ordersSpec, `"OrderInput": {"type": "object", "properties"`, `"OrderInput": {"type": "object", "required": ["reference"], "properties"`, 1)
	def, err := importer.NormalizeOpenAPI([]byte(strict))
	if err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, def, Config{ID: "sbx_strict", Seed: "v-1"})
	got := do(t, e, "POST", "/orders", `{"amount":5}`, nil)
	if got.status < 400 || got.status > 499 || !strings.Contains(got.headers[violationsHeader], "reference is required") {
		t.Fatalf("a declared required field must be enforced: %d %v", got.status, got.headers)
	}
	loose := ordersEngine(t, "v-2")
	if got := do(t, loose, "POST", "/orders", `{"amount":5}`, nil); got.status != 201 {
		t.Fatalf("with nothing declared nothing is enforced: %d %s", got.status, got.body)
	}
}

func TestPaymentsFixtureCreateLooksReal(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/parity/importer/specs/paystack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	def, err := importer.NormalizeOpenAPI(raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	e := newEngine(t, def, Config{ID: "sbx_pay", Seed: "pay-1"})
	e.SetMountPrefix("/pay")
	got := do(t, e, "POST", "/customer", `{"email":"a@b.test","first_name":"Ada"}`, map[string]string{"Authorization": "Bearer " + e.Credential()})
	if got.status != 200 {
		t.Fatalf("create customer: %d %s", got.status, got.body)
	}
	b := bodyOf(t, got)
	data, _ := b["data"].(map[string]any)
	if b["status"] != true {
		t.Errorf("status = %v, want true on a success", b["status"])
	}
	if data == nil {
		t.Fatalf("no data envelope: %v", b)
	}
	if data["email"] != "a@b.test" || data["first_name"] != "Ada" {
		t.Errorf("request fields must be echoed: %v", data)
	}
	if !isISO(asString(data["createdAt"])) {
		t.Errorf("createdAt = %v, want ISO 8601", data["createdAt"])
	}
	if !strings.HasPrefix(asString(data["customer_code"]), "cus_") {
		t.Errorf("customer_code = %v, want a cus_ token", data["customer_code"])
	}
}

func TestRealismLintCountsFallbackFields(t *testing.T) {
	count, names := RealismLint(loadOrders(t))
	if count != 1 || len(names) != 1 || names[0] != "note" {
		t.Fatalf("orders has one field with no source, got %d %v", count, names)
	}
	clean := strings.Replace(ordersSpec, `"note": {"type": "string"}`, `"note": {"type": "string", "example": "n/a"}`, 1)
	def, err := importer.NormalizeOpenAPI([]byte(clean))
	if err != nil {
		t.Fatal(err)
	}
	if count, names := RealismLint(def); count != 0 || len(names) != 0 {
		t.Fatalf("an example is a source, got %d %v", count, names)
	}
}
