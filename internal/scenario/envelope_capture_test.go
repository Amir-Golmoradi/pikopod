package scenario

import (
	"testing"

	"github.com/pikopod/pikopod/internal/importer"
	"github.com/pikopod/pikopod/internal/sandbox"
)

const envelopeSpec = `{
  "openapi": "3.1.0",
  "info": {"title": "Things", "version": "1.0.0"},
  "paths": {
    "/things": {
      "post": {
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/ThingInput"}}}},
        "responses": {"201": {"description": "created", "content": {"application/json": {"schema": {
          "type": "object",
          "properties": {"status": {"type": "boolean"}, "message": {"type": "string"}, "data": {"$ref": "#/components/schemas/Thing"}}
        }}}}}
      }
    },
    "/things/{id}": {
      "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
      "patch": {
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/ThingInput"}}}},
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {
          "type": "object",
          "properties": {"status": {"type": "boolean"}, "data": {"$ref": "#/components/schemas/Thing"}}
        }}}}}
      }
    }
  },
  "components": {"schemas": {
    "ThingInput": {"type": "object", "properties": {"name": {"type": "string"}, "state": {"type": "string", "enum": ["draft", "active"]}}},
    "Thing": {"type": "object", "properties": {"id": {"type": "string"}, "name": {"type": "string"}, "state": {"type": "string", "enum": ["draft", "active"]}}}
  }}
}`

const envelopeDef = `{
  "steps": [
    {"key": "create", "type": "REQUEST",
     "capture": {"rid": "response.body$.id"},
     "assertions": [
       {"target": "response.status", "op": "equals", "expected": 201},
       {"target": "response.body", "path": "$.name", "op": "equals", "expected": "gizmo"}
     ],
     "config": {"method": "POST", "path": "/things", "body": {"name": "gizmo", "state": "draft"}}},
    {"key": "transition", "type": "REQUEST",
     "assertions": [{"target": "response.body", "path": "$.state", "op": "equals", "expected": "active"}],
     "config": {"method": "PATCH", "path": "/things/{{rid}}", "body": {"state": "active"}}},
    {"key": "verify", "type": "ASSERT_STATE",
     "assertions": [{"target": "state.resource", "path": "$.state", "op": "equals", "expected": "active"}],
     "config": {"resourceType": "/things", "resourceId": "{{rid}}"}}
  ]
}`

func TestCapturesAndAssertionsResolveThroughTheEnvelope(t *testing.T) {
	def, err := importer.NormalizeOpenAPI([]byte(envelopeSpec))
	if err != nil {
		t.Fatal(err)
	}
	store, err := sandbox.OpenMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	eng, err := sandbox.NewEngine(def, sandbox.Config{ID: "sbx_env", Seed: "env-1"}, store)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(eng, parseDef(t, envelopeDef), nil, "env-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != RunPassed {
		t.Fatalf("run should pass through the data envelope, got %s (%s)\n%+v", res.Status, res.Summary, res.Steps)
	}
	resolved, _ := res.Steps[0].Detail["captures"].(map[string]string)
	if resolved["rid"] != "$.data.id" {
		t.Fatalf("the step must say which path the capture resolved through, got %v", res.Steps[0].Detail["captures"])
	}
}
