package archetype

import (
	"testing"

	"github.com/pikopod/pikopod/internal/importer"
)

const requiredSpec = `{
  "openapi": "3.1.0",
  "info": {"title": "Strict", "version": "1.0.0"},
  "paths": {
    "/charges": {
      "post": {
        "operationId": "createCharge",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ChargeInput"}}}},
        "responses": {
          "201": {"description": "created", "content": {"application/json": {"schema": {"type": "object", "properties": {"id": {"type": "string"}, "status": {"type": "string", "enum": ["pending", "paid"]}}}}}},
          "400": {"description": "bad", "content": {"application/json": {"schema": {"type": "object", "properties": {"message": {"type": "string"}}}}}}
        }
      }
    }
  },
  "components": {"schemas": {"ChargeInput": {
    "type": "object", "required": ["amount", "currency", "customer"],
    "properties": {
      "amount": {"type": "integer"},
      "currency": {"type": "string", "enum": ["NGN", "USD"]},
      "customer": {"type": "object", "required": ["email"], "properties": {"email": {"type": "string", "format": "email"}, "name": {"type": "string"}}},
      "note": {"type": "string"}
    }
  }}}
}`

func TestExpandFillsRequiredRequestFields(t *testing.T) {
	def, err := importer.NormalizeOpenAPI([]byte(requiredSpec))
	if err != nil {
		t.Fatal(err)
	}
	var declines *Archetype
	for i := range Extensions {
		if Extensions[i].ID == "declines" {
			declines = &Extensions[i]
		}
	}
	if declines == nil {
		t.Fatal("declines archetype missing")
	}
	exp, err := Expand(declines, map[string]string{"op": "createCharge"}, def)
	if err != nil {
		t.Fatal(err)
	}
	steps := exp.Definition["steps"].([]any)
	var body map[string]any
	for _, s := range steps {
		m := s.(map[string]any)
		if m["type"] == "REQUEST" {
			body = m["config"].(map[string]any)["body"].(map[string]any)
			break
		}
	}
	if body["amount"] != 1 || body["currency"] != "NGN" {
		t.Fatalf("required scalars must be filled from the schema: %v", body)
	}
	customer, _ := body["customer"].(map[string]any)
	if customer["email"] != "user@example.test" {
		t.Fatalf("nested required fields must be filled: %v", body)
	}
	if _, has := body["note"]; has {
		t.Fatalf("optional fields must not be invented: %v", body)
	}
	if _, has := customer["name"]; has {
		t.Fatalf("optional nested fields must not be invented: %v", body)
	}
}
