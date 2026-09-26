package scenario

import (
	"strings"
	"testing"
)

const faultBodyDef = `{
  "steps": [
    {"key": "arm", "type": "INJECT_FAULT",
     "config": {"method": "POST", "path": "/widgets", "kind": "error", "status": 402, "times": 1,
                "body": {"error": {"code": "card_declined"}}, "headers": {"x-request-id": "req_fbd_1"}}},
    {"key": "declined", "type": "REQUEST",
     "assertions": [
       {"target": "response.status", "op": "equals", "expected": 402},
       {"target": "response.body", "path": "$.error.code", "op": "equals", "expected": "card_declined"},
       {"target": "response.headers", "key": "x-request-id", "op": "equals", "expected": "req_fbd_1"}
     ],
     "config": {"method": "POST", "path": "/widgets", "body": {"name": "g"}}},
    {"key": "recovered", "type": "REQUEST",
     "assertions": [{"target": "response.status", "op": "equals", "expected": 201}],
     "config": {"method": "POST", "path": "/widgets", "body": {"name": "g"}}}
  ]
}`

func TestInjectFaultBodyReachesTheRequestStep(t *testing.T) {
	def := parseDef(t, faultBodyDef)
	cfg := def.Steps[0].Config.(*InjectFaultConfig)
	rule, ok := FaultRuleFor(cfg, "t")
	if !ok || string(rule.Body) != `{"error":{"code":"card_declined"}}` || rule.Headers["x-request-id"] != "req_fbd_1" {
		t.Fatalf("FaultRuleFor dropped the body or headers: ok=%v rule=%+v", ok, rule)
	}

	res, err := Run(runnerEngine(t, "fb-1"), def, nil, "fb-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != RunPassed {
		t.Fatalf("run should pass, got %s (%s)\n%+v", res.Status, res.Summary, res.Steps)
	}
}

func TestInjectFaultBodyMustBeJSONObjectOrArray(t *testing.T) {
	_, errs := ParseDefinitionJSON([]byte(`{"steps": [{"key": "arm", "type": "INJECT_FAULT",
	  "config": {"method": "POST", "path": "/widgets", "kind": "error", "body": "declined"}}]}`))
	if len(errs) == 0 {
		t.Fatal("a string body must be refused")
	}
	if !strings.Contains(errs[0].Pointer, "/body") {
		t.Fatalf("refusal should point at body, got %+v", errs[0])
	}
	_, errs = ParseDefinitionJSON([]byte(`{"steps": [{"key": "arm", "type": "INJECT_FAULT",
	  "config": {"method": "POST", "path": "/widgets", "kind": "error", "headers": {"x": 1}}}]}`))
	if len(errs) == 0 {
		t.Fatal("a non-string header value must be refused")
	}
}
