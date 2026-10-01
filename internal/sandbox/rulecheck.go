package sandbox

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/ir"
	"gopkg.in/yaml.v3"
)

type RuleFinding struct {
	RuleID  string `json:"ruleId"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type RuleDescription struct {
	ID         string `json:"id"`
	When       string `json:"when"`
	Respond    string `json:"respond"`
	Provenance string `json:"provenance"`
	Version    int    `json:"version"`
}

func (r *compiledRule) unconditional() bool {
	return len(r.body) == 0 && len(r.state) == 0 && r.When.Times == 0
}

func whenKey(r *compiledRule) string {
	raw, _ := json.Marshal(r.When)
	return r.When.Method + " " + r.When.Path + " " + string(raw)
}

func declaredSuccess(endpoint *ir.Endpoint) string {
	for _, r := range endpoint.Responses {
		if len(r.StatusCode) == 3 && r.StatusCode[0] == '2' {
			return r.StatusCode
		}
	}
	return "2xx"
}

func storedTypes(def *ir.ApiDefinition) map[string]bool {
	out := map[string]bool{}
	for i := range def.Endpoints {
		op := deriveOperation(&def.Endpoints[i], nil)
		if op.typ != "" {
			out[op.typ] = true
		}
	}
	return out
}

func CheckRules(def *ir.ApiDefinition, rs *RuleSet) []RuleFinding {
	compiled, err := compileRules(def, rs)
	if err != nil {
		return []RuleFinding{{Kind: "invalid", Message: err.Error()}}
	}
	types := storedTypes(def)
	var out []RuleFinding
	firstUnconditional := map[*ir.Endpoint]string{}
	seenWhen := map[string]string{}
	for i := range compiled {
		r := &compiled[i]
		op := r.When.Method + " " + r.When.Path
		if winner, shadowed := firstUnconditional[r.endpoint]; shadowed {
			out = append(out, RuleFinding{RuleID: r.ID, Kind: "never-fires", Message: fmt.Sprintf("%s fires on every %s before it, so this rule never runs", winner, op)})
			continue
		}
		if twin, dup := seenWhen[whenKey(r)]; dup {
			out = append(out, RuleFinding{RuleID: r.ID, Kind: "never-fires", Message: fmt.Sprintf("%s has the same conditions and comes first, so this rule never runs", twin)})
			continue
		}
		seenWhen[whenKey(r)] = r.ID
		ghost := ""
		for _, tc := range r.state {
			if !types[tc.typ] {
				ghost = tc.typ
				break
			}
		}
		if ghost != "" {
			out = append(out, RuleFinding{RuleID: r.ID, Kind: "never-fires", Message: fmt.Sprintf("its state condition names %s, which no operation in the spec stores, so it can never hold", ghost)})
			continue
		}
		if r.unconditional() {
			firstUnconditional[r.endpoint] = r.ID
			out = append(out, RuleFinding{RuleID: r.ID, Kind: "shadows", Message: fmt.Sprintf("fires on every %s, so the declared %s response is never served by the operation", op, declaredSuccess(r.endpoint))})
		}
	}
	return out
}

func DescribeRules(def *ir.ApiDefinition, rs *RuleSet) ([]RuleDescription, error) {
	compiled, err := compileRules(def, rs)
	if err != nil {
		return nil, err
	}
	out := make([]RuleDescription, 0, len(compiled))
	for i := range compiled {
		r := &compiled[i]
		out = append(out, RuleDescription{ID: r.ID, When: r.summary(), Respond: respondSummary(&r.Respond), Provenance: r.Provenance, Version: r.Version})
	}
	return out, nil
}

func respondSummary(r *RuleRespond) string {
	s := strconv.Itoa(r.Status)
	switch {
	case len(r.Body) > 0:
		s += " body"
	case r.Example != 0:
		s += " example"
	}
	if r.SetState != nil {
		s += ", set " + r.SetState.Resource + " " + r.SetState.State
	}
	if r.Emit != "" {
		s += ", emit " + r.Emit
	}
	return s
}

func ParseRuleFile(raw []byte) ([]Rule, error) {
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, errfmt.Newf("the rule file is not YAML or JSON", "fix the file", rulesDocs, "%v", err)
	}
	normalised, err := json.Marshal(doc)
	if err != nil {
		return nil, errfmt.Newf("the rule file does not convert to JSON", "use string keys throughout", rulesDocs, "%v", err)
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, errfmt.New("the rule file holds no rule", "it must be one rule object or {\"rules\": [...]}", "see the Rules page for the shape", rulesDocs)
	}
	var rules []Rule
	if _, has := obj["rules"]; has {
		var set RuleSet
		if err := json.Unmarshal(normalised, &set); err != nil {
			return nil, errfmt.Newf("the rules list does not parse", "check each rule's when and respond", rulesDocs, "%v", err)
		}
		rules = set.Rules
	} else if _, has := obj["id"]; has {
		var one Rule
		if err := json.Unmarshal(normalised, &one); err != nil {
			return nil, errfmt.Newf("the rule does not parse", "check when and respond", rulesDocs, "%v", err)
		}
		rules = []Rule{one}
	}
	if len(rules) == 0 {
		return nil, errfmt.New("the rule file holds no rule", "it must be one rule object with an id, or {\"rules\": [...]}", "see the Rules page for the shape", rulesDocs)
	}
	for i := range rules {
		if rules[i].Provenance == "" {
			rules[i].Provenance = "manual"
		}
	}
	return rules, nil
}

func (e *Engine) SetRules(rs *RuleSet) error {
	compiled, err := compileRules(e.def, rs)
	if err != nil {
		return err
	}
	e.rulesMu.Lock()
	defer e.rulesMu.Unlock()
	e.rules = compiled
	e.rulesVersion = 0
	if rs != nil {
		e.rulesVersion = rs.Version
	}
	return nil
}

func (e *Engine) RuleSet() *RuleSet {
	e.rulesMu.Lock()
	defer e.rulesMu.Unlock()
	out := &RuleSet{Version: e.rulesVersion, Rules: make([]Rule, 0, len(e.rules))}
	for i := range e.rules {
		out.Rules = append(out.Rules, e.rules[i].Rule)
	}
	return out
}
