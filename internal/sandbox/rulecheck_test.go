package sandbox

import (
	"strings"
	"testing"
)

func findingKinds(fs []RuleFinding) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.RuleID+":"+f.Kind)
	}
	return strings.Join(out, " ")
}

func TestCheckRulesReportsUnconditionalShadowing(t *testing.T) {
	def := loadAccounts(t)
	rs := &RuleSet{Rules: []Rule{
		rule("always-422", "POST", "/accounts", nil, map[string]any{"status": 422, "body": map[string]any{"error": "no"}}),
		rule("guarded", "POST", "/accounts", map[string]any{"body": map[string]any{"display_name": "exists_in_store"}}, map[string]any{"status": 422, "body": map[string]any{"error": "taken"}}),
	}}
	fs := CheckRules(def, rs)
	if got := findingKinds(fs); got != "always-422:shadows guarded:never-fires" {
		t.Fatalf("findings: %q", got)
	}
	if !strings.Contains(fs[0].Message, "every POST /accounts") || !strings.Contains(fs[0].Message, "201") {
		t.Fatalf("shadow finding must name the operation and the declared success it hides: %q", fs[0].Message)
	}
	if !strings.Contains(fs[1].Message, "always-422") {
		t.Fatalf("never-fires finding must name the rule that wins first: %q", fs[1].Message)
	}
}

func TestCheckRulesIsQuietForConditionalRules(t *testing.T) {
	def := loadAccounts(t)
	rs := &RuleSet{Rules: []Rule{
		rule("unique", "POST", "/accounts", map[string]any{"body": map[string]any{"display_name": "exists_in_store"}}, map[string]any{"status": 422, "body": map[string]any{"error": "taken"}}),
		rule("twice", "POST", "/accounts", map[string]any{"times": 2}, map[string]any{"status": 503}),
		rule("frozen", "GET", "/accounts/{accountId}", map[string]any{"state": map[string]any{"/accounts": map[string]any{"state_is": "frozen"}}}, map[string]any{"status": 423}),
	}}
	if fs := CheckRules(def, rs); len(fs) != 0 {
		t.Fatalf("conditional rules are fine: %q", findingKinds(fs))
	}
}

func TestCheckRulesReportsRulesThatCanNeverHold(t *testing.T) {
	def := loadAccounts(t)
	rs := &RuleSet{Rules: []Rule{
		rule("ghost-type", "GET", "/accounts/{accountId}", map[string]any{"state": map[string]any{"/ledgers": map[string]any{"state_is": "open"}}}, map[string]any{"status": 423}),
		rule("first", "POST", "/accounts", map[string]any{"body": map[string]any{"tier": map[string]any{"equals": "gold"}}}, map[string]any{"status": 402, "example": 402}),
		rule("twin", "POST", "/accounts", map[string]any{"body": map[string]any{"tier": map[string]any{"equals": "gold"}}}, map[string]any{"status": 418}),
	}}
	fs := CheckRules(def, rs)
	if got := findingKinds(fs); got != "ghost-type:never-fires twin:never-fires" {
		t.Fatalf("findings: %q", got)
	}
	if !strings.Contains(fs[0].Message, "/ledgers") {
		t.Fatalf("a state condition on a type no operation stores must be named: %q", fs[0].Message)
	}
	if !strings.Contains(fs[1].Message, "first") {
		t.Fatalf("an identical earlier rule must be named: %q", fs[1].Message)
	}
}

func TestDescribeRulesSummarisesEachRule(t *testing.T) {
	def := loadAccounts(t)
	rs := &RuleSet{Version: 3, Rules: []Rule{
		{ID: "unique", Version: 1, Provenance: "manual", When: rule("unique", "POST", "/accounts", map[string]any{"body": map[string]any{"display_name": "exists_in_store"}}, nil).When,
			Respond: rule("", "", "", nil, map[string]any{"status": 422, "body": map[string]any{"error": "taken"}}).Respond},
		{ID: "freeze", Version: 2, Provenance: "promoted:fp_abc", When: rule("freeze", "PATCH", "/accounts/{accountId}", map[string]any{"body": map[string]any{"tier": map[string]any{"equals": "frozen"}}}, nil).When,
			Respond: rule("", "", "", nil, map[string]any{"status": 202, "example": 0, "set_state": map[string]any{"resource": "/accounts", "state": "frozen"}, "emit": "accounts.frozen"}).Respond},
	}}
	rows, err := DescribeRules(def, rs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: %d", len(rows))
	}
	if rows[0].When != "POST /accounts when body.display_name exists_in_store" || rows[0].Respond != "422 body" || rows[0].Provenance != "manual" || rows[0].Version != 1 {
		t.Fatalf("row 0: %+v", rows[0])
	}
	if rows[1].When != `PATCH /accounts/{accountId} when body.tier equals "frozen"` || rows[1].Respond != "202, set /accounts frozen, emit accounts.frozen" || rows[1].Provenance != "promoted:fp_abc" {
		t.Fatalf("row 1: %+v", rows[1])
	}
}

func TestSetRulesReplacesTheLiveSetOrRefuses(t *testing.T) {
	e := mustRulesEngine(t, "hot-1")
	if got := post(t, e, `{"display_name":"a"}`); got.status != 201 {
		t.Fatalf("no rules yet: %d", got.status)
	}
	good := &RuleSet{Version: 2, Rules: []Rule{rule("teapot", "POST", "/accounts", nil, map[string]any{"status": 418})}}
	if err := e.SetRules(good); err != nil {
		t.Fatal(err)
	}
	if got := post(t, e, `{"display_name":"a"}`); got.status != 418 {
		t.Fatalf("the new set must answer at once: %d", got.status)
	}
	bad := &RuleSet{Rules: []Rule{rule("ghost", "POST", "/nope", nil, map[string]any{"status": 500})}}
	if err := e.SetRules(bad); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("a bad set must be refused by name: %v", err)
	}
	if got := post(t, e, `{"display_name":"a"}`); got.status != 418 {
		t.Fatalf("a refused set must leave the live rules alone: %d", got.status)
	}
	if rules := e.Rules(); len(rules) != 1 || rules[0].ID != "teapot" {
		t.Fatalf("Rules() must reflect the live set: %+v", rules)
	}
}

func TestParseRuleFileAcceptsYAMLOrJSONOneOrMany(t *testing.T) {
	one, err := ParseRuleFile([]byte("id: unique\nwhen:\n  method: POST\n  path: /accounts\n  body:\n    display_name: exists_in_store\nrespond:\n  status: 422\n  body:\n    error: taken\n"))
	if err != nil || len(one) != 1 || one[0].ID != "unique" || one[0].Provenance != "manual" || one[0].Respond.Status != 422 {
		t.Fatalf("yaml single rule: %+v %v", one, err)
	}
	many, err := ParseRuleFile([]byte(`{"rules":[{"id":"a","when":{"method":"GET","path":"/accounts/{accountId}"},"respond":{"status":404}},{"id":"b","provenance":"imported:fp_1","when":{"method":"GET","path":"/accounts/{accountId}"},"respond":{"status":410}}]}`))
	if err != nil || len(many) != 2 || many[1].Provenance != "imported:fp_1" {
		t.Fatalf("json list: %+v %v", many, err)
	}
	if _, err := ParseRuleFile([]byte("just: words\n")); err == nil || !strings.Contains(err.Error(), "rule") {
		t.Fatalf("a file with no rule must be refused: %v", err)
	}
}
