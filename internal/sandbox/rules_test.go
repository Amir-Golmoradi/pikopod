package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/importer"
	"github.com/pikopod/pikopod/internal/ir"
)

const accountsSpec = `{
  "openapi": "3.1.0",
  "info": {"title": "Accounts", "version": "1.0.0"},
  "webhooks": {
    "accounts.frozen": {"post": {"requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Account"}}}}, "responses": {"200": {"description": "ack"}}}}
  },
  "paths": {
    "/accounts": {
      "post": {
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Account"}}}},
        "responses": {
          "201": {"description": "created", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Account"}}}},
          "402": {"description": "needs payment", "content": {"application/json": {
            "schema": {"type": "object", "properties": {"error": {"type": "string"}}},
            "example": {"error": "payment_required"}
          }}}
        }
      }
    },
    "/accounts/{accountId}": {
      "parameters": [{"name": "accountId", "in": "path", "required": true, "schema": {"type": "string"}}],
      "get": {"responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Account"}}}}}},
      "patch": {
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Account"}}}},
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Account"}}}}}
      }
    }
  },
  "components": {"schemas": {"Account": {
    "type": "object",
    "properties": {"id": {"type": "string"}, "display_name": {"type": "string"}, "tier": {"type": "string"}}
  }}}
}`

func loadAccounts(t *testing.T) *ir.ApiDefinition {
	t.Helper()
	def, err := importer.NormalizeOpenAPI([]byte(accountsSpec))
	if err != nil {
		t.Fatalf("normalize accounts spec: %v", err)
	}
	return def
}

func rule(id, method, path string, when map[string]any, respond map[string]any) Rule {
	whenRaw, _ := json.Marshal(when)
	var w RuleWhen
	json.Unmarshal(whenRaw, &w)
	w.Method, w.Path = method, path
	respRaw, _ := json.Marshal(respond)
	var r RuleRespond
	json.Unmarshal(respRaw, &r)
	return Rule{ID: id, When: w, Respond: r, Provenance: "manual"}
}

func rulesEngine(t *testing.T, seed string, rules ...Rule) (*Engine, error) {
	t.Helper()
	store, err := OpenMemoryStore()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewEngine(loadAccounts(t), Config{ID: "sbx_rules", Seed: seed, Rules: &RuleSet{Version: 1, Rules: rules}}, store)
}

func mustRulesEngine(t *testing.T, seed string, rules ...Rule) *Engine {
	t.Helper()
	e, err := rulesEngine(t, seed, rules...)
	if err != nil {
		t.Fatalf("engine with rules: %v", err)
	}
	return e
}

func post(t *testing.T, e *Engine, body string) recorded {
	t.Helper()
	return do(t, e, "POST", "/accounts", body, nil)
}

func TestRuleMatchersOnTheRequestBody(t *testing.T) {
	cases := []struct {
		name    string
		matcher any
		body    string
		fires   bool
	}{
		{"exists hits", "exists", `{"display_name":"a"}`, true},
		{"exists misses", "exists", `{"tier":"gold"}`, false},
		{"absent hits", "absent", `{"tier":"gold"}`, true},
		{"absent misses", "absent", `{"display_name":"a"}`, false},
		{"equals hits", map[string]any{"equals": "acme"}, `{"display_name":"acme"}`, true},
		{"equals misses", map[string]any{"equals": "acme"}, `{"display_name":"other"}`, false},
		{"equals number", map[string]any{"equals": 3}, `{"display_name":3}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := mustRulesEngine(t, "m-1", rule("r", "POST", "/accounts",
				map[string]any{"body": map[string]any{"display_name": tc.matcher}},
				map[string]any{"status": 418, "body": map[string]any{"rule": "fired"}}))
			got := post(t, e, tc.body)
			if tc.fires && (got.status != 418 || got.headers["x-pikopod-rule"] != "r") {
				t.Fatalf("rule should fire: %d %v", got.status, got.headers)
			}
			if !tc.fires && got.status != 201 {
				t.Fatalf("rule must not fire: %d %s", got.status, got.body)
			}
		})
	}
}

func TestRuleExistsInStoreMakesDisplayNameUnique(t *testing.T) {
	e := mustRulesEngine(t, "u-1", rule("unique-name", "POST", "/accounts",
		map[string]any{"body": map[string]any{"display_name": "exists_in_store"}},
		map[string]any{"status": 422, "body": map[string]any{"error": "display_name already taken"}}))

	first := post(t, e, `{"display_name":"acme"}`)
	if first.status != 201 {
		t.Fatalf("first create must pass: %d %s", first.status, first.body)
	}
	second := post(t, e, `{"display_name":"acme"}`)
	if second.status != 422 {
		t.Fatalf("second create with the same name must be refused by the rule: %d %s", second.status, second.body)
	}
	sameJSON(t, second.body, `{"error":"display_name already taken"}`)
	if second.headers["content-type"] != jsonContentType || second.headers["x-pikopod-rule"] != "unique-name" {
		t.Fatalf("rule response headers: %v", second.headers)
	}
	third := post(t, e, `{"display_name":"other"}`)
	if third.status != 201 {
		t.Fatalf("a different name must still create: %d %s", third.status, third.body)
	}
	if got := do(t, e, "GET", "/accounts/accounts_1", "", nil); got.status != 200 {
		t.Fatalf("the refused create must not have stored anything: %d", got.status)
	}
}

func TestRulesAreRefusedAtLoadWhenTheyCannotBeTrue(t *testing.T) {
	cases := []struct {
		name string
		r    Rule
		want string
	}{
		{"undeclared route", rule("ghost", "POST", "/nope", nil, map[string]any{"status": 500}), "does not declare"},
		{"undeclared method", rule("ghost", "DELETE", "/accounts", nil, map[string]any{"status": 500}), "does not declare"},
		{"undeclared event", rule("evt", "POST", "/accounts", nil, map[string]any{"status": 201, "emit": "accounts.exploded"}), "not declared"},
		{"undeclared example", rule("ex", "POST", "/accounts", nil, map[string]any{"status": 409, "example": 409}), "no example"},
		{"body and example", rule("both", "POST", "/accounts", nil, map[string]any{"status": 402, "example": 402, "body": map[string]any{"x": 1}}), "either"},
		{"unknown matcher", rule("m", "POST", "/accounts", map[string]any{"body": map[string]any{"display_name": "sometimes"}}, map[string]any{"status": 400}), "matcher"},
		{"bad provenance", func() Rule {
			r := rule("p", "POST", "/accounts", nil, map[string]any{"status": 400})
			r.Provenance = "guessed"
			return r
		}(), "provenance"},
		{"empty id", rule("", "POST", "/accounts", nil, map[string]any{"status": 400}), "id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rulesEngine(t, "l-1", tc.r)
			if err == nil {
				t.Fatal("rule must be refused at load")
			}
			if !strings.Contains(err.Error(), tc.want) || (tc.r.ID != "" && !strings.Contains(err.Error(), tc.r.ID)) {
				t.Fatalf("refusal should name the rule and the reason %q, got: %v", tc.want, err)
			}
		})
	}
	dup := []Rule{rule("same", "POST", "/accounts", nil, map[string]any{"status": 400}), rule("same", "POST", "/accounts", nil, map[string]any{"status": 401})}
	if _, err := rulesEngine(t, "l-2", dup...); err == nil || !strings.Contains(err.Error(), "same") {
		t.Fatalf("duplicate ids must be refused: %v", err)
	}
}

func TestRuleEmitsOnlyDeclaredEvents(t *testing.T) {
	e := mustRulesEngine(t, "e-1", rule("freeze", "PATCH", "/accounts/{accountId}",
		map[string]any{"body": map[string]any{"tier": map[string]any{"equals": "frozen"}}},
		map[string]any{"status": 202, "emit": "accounts.frozen"}))
	if got := post(t, e, `{"display_name":"acme"}`); got.status != 201 {
		t.Fatalf("seed create: %d", got.status)
	}
	if got := do(t, e, "PATCH", "/accounts/accounts_1", `{"tier":"frozen"}`, nil); got.status != 202 {
		t.Fatalf("rule should fire: %d %s", got.status, got.body)
	}
	if n := len(e.Deliveries("accounts.frozen")); n != 1 {
		t.Fatalf("the declared event must be delivered once, got %d", n)
	}
}

func TestRuleAnswersWithTheDeclaredExample(t *testing.T) {
	e := mustRulesEngine(t, "x-1", rule("pay-first", "POST", "/accounts",
		map[string]any{"body": map[string]any{"tier": map[string]any{"equals": "gold"}}},
		map[string]any{"status": 402, "example": 402, "headers": map[string]any{"x-upgrade": "required"}}))
	got := post(t, e, `{"display_name":"acme","tier":"gold"}`)
	if got.status != 402 {
		t.Fatalf("rule should fire: %d %s", got.status, got.body)
	}
	sameJSON(t, got.body, `{"error":"payment_required"}`)
	if got.headers["x-upgrade"] != "required" || got.headers["content-type"] != jsonContentType {
		t.Fatalf("headers: %v", got.headers)
	}
}

func TestRuleStateConditionsAndSetState(t *testing.T) {
	e := mustRulesEngine(t, "s-1",
		rule("freeze", "PATCH", "/accounts/{accountId}",
			map[string]any{"body": map[string]any{"tier": map[string]any{"equals": "frozen"}}},
			map[string]any{"status": 200, "body": map[string]any{"frozen": true}, "set_state": map[string]any{"resource": "/accounts", "state": "frozen"}}),
		rule("read-frozen", "GET", "/accounts/{accountId}",
			map[string]any{"state": map[string]any{"/accounts": map[string]any{"state_is": "frozen"}}},
			map[string]any{"status": 423, "body": map[string]any{"error": "account frozen"}}),
		rule("never-on-live", "PATCH", "/accounts/{accountId}",
			map[string]any{"state": map[string]any{"/accounts": map[string]any{"state_not": "frozen"}}, "body": map[string]any{"tier": map[string]any{"equals": "live-check"}}},
			map[string]any{"status": 204}))
	post(t, e, `{"display_name":"acme"}`)
	if got := do(t, e, "GET", "/accounts/accounts_1", "", nil); got.status != 200 {
		t.Fatalf("no state yet, the read must pass through: %d", got.status)
	}
	if got := do(t, e, "PATCH", "/accounts/accounts_1", `{"tier":"live-check"}`, nil); got.status != 204 {
		t.Fatalf("state_not must match a resource whose state is not frozen: %d %s", got.status, got.body)
	}
	if got := do(t, e, "PATCH", "/accounts/accounts_1", `{"tier":"frozen"}`, nil); got.status != 200 {
		t.Fatalf("freeze rule should fire: %d %s", got.status, got.body)
	}
	got := do(t, e, "GET", "/accounts/accounts_1", "", nil)
	if got.status != 423 {
		t.Fatalf("state_is frozen must now match: %d %s", got.status, got.body)
	}
	if got := do(t, e, "PATCH", "/accounts/accounts_1", `{"tier":"live-check"}`, nil); got.status == 204 {
		t.Fatalf("state_not frozen must not match a frozen resource")
	}
	if got := do(t, e, "GET", "/accounts/accounts_9", "", nil); got.status != 404 {
		t.Fatalf("a state condition on a missing resource must not fire: %d", got.status)
	}
}

func TestRuleTimesWindowReusesFaultWindows(t *testing.T) {
	e := mustRulesEngine(t, "w-1", rule("twice", "POST", "/accounts",
		map[string]any{"times": 2, "per": "idempotency-key"},
		map[string]any{"status": 503}))
	h := map[string]string{"Idempotency-Key": "k1"}
	got := []int{post2(t, e, h), post2(t, e, h), post2(t, e, h)}
	if got[0] != 503 || got[1] != 503 || got[2] != 201 {
		t.Fatalf("window per key: %v", got)
	}
	other := map[string]string{"Idempotency-Key": "k2"}
	if s := post2(t, e, other); s != 503 {
		t.Fatalf("another key has its own window: %d", s)
	}
}

func post2(t *testing.T, e *Engine, headers map[string]string) int {
	t.Helper()
	return do(t, e, "POST", "/accounts", `{"display_name":"n"}`, headers).status
}

func TestRuleFiresAfterAuthAndFaults(t *testing.T) {
	e := mustRulesEngine(t, "o-1", rule("any", "POST", "/accounts", nil, map[string]any{"status": 418}))
	e.ArmFault(FaultRule{Method: "POST", Path: "/accounts", Kind: "error", Status: 503, Probability: 1, Times: 1})
	if got := post(t, e, `{}`); got.status != 503 {
		t.Fatalf("an armed fault answers before any rule: %d", got.status)
	}
	if got := post(t, e, `{}`); got.status != 418 {
		t.Fatalf("with the fault spent the rule answers: %d", got.status)
	}
	var stages []string
	e.SetTrace(func(stage, message string) { stages = append(stages, stage+": "+message) })
	post(t, e, `{}`)
	joined := strings.Join(stages, "\n")
	if !strings.Contains(joined, "rules: rule any fired: POST /accounts") {
		t.Fatalf("trace must name the rule that fired:\n%s", joined)
	}
}

func TestRuleSetRoundTripsAtomically(t *testing.T) {
	dir := t.TempDir()
	path := RulesPath(dir, "acct")
	if !strings.HasSuffix(path, filepath.Join("apis", "acct.rules.json")) {
		t.Fatalf("rules live beside the IR: %s", path)
	}
	rs, err := LoadRuleSet(path)
	if err != nil || rs == nil || len(rs.Rules) != 0 || rs.Version != 0 {
		t.Fatalf("a missing file is an empty set: %+v %v", rs, err)
	}
	rs.Rules = append(rs.Rules, rule("unique-name", "POST", "/accounts",
		map[string]any{"body": map[string]any{"display_name": "exists_in_store"}},
		map[string]any{"status": 422, "body": map[string]any{"error": "taken"}}))
	if err := SaveRuleSet(path, rs); err != nil {
		t.Fatal(err)
	}
	again, err := LoadRuleSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != 1 || len(again.Rules) != 1 || again.Rules[0].Version != 1 || again.Rules[0].ID != "unique-name" {
		t.Fatalf("save must stamp the set and the rule with the new version: %+v", again)
	}
	if err := SaveRuleSet(path, again); err != nil {
		t.Fatal(err)
	}
	third, _ := LoadRuleSet(path)
	if third.Version != 2 || third.Rules[0].Version != 1 {
		t.Fatalf("an unchanged rule keeps its version while the set advances: %+v", third)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuleSet(path); err == nil || !strings.Contains(err.Error(), "rules file") {
		t.Fatalf("a corrupt file is refused with a named error, got %v", err)
	}
}
