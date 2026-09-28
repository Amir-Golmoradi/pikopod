package sandbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/ir"
	"github.com/pikopod/pikopod/internal/store"
)

const rulesDocs = "docs/config-reference.md#rules"

type RuleSet struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

type Rule struct {
	ID         string      `json:"id"`
	When       RuleWhen    `json:"when"`
	Respond    RuleRespond `json:"respond"`
	Provenance string      `json:"provenance"`
	Version    int         `json:"version"`
}

type RuleWhen struct {
	Method string                     `json:"method"`
	Path   string                     `json:"path"`
	Body   map[string]json.RawMessage `json:"body,omitempty"`
	State  map[string]json.RawMessage `json:"state,omitempty"`
	Times  int                        `json:"times,omitempty"`
	Per    string                     `json:"per,omitempty"`
}

type RuleRespond struct {
	Status   int               `json:"status"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     json.RawMessage   `json:"body,omitempty"`
	Example  int               `json:"example,omitempty"`
	SetState *RuleSetState     `json:"set_state,omitempty"`
	Emit     string            `json:"emit,omitempty"`
}

type RuleSetState struct {
	Resource string `json:"resource"`
	State    string `json:"state"`
}

type bodyMatcher struct {
	kind  string
	value json.RawMessage
}

type stateCond struct {
	kind  string
	value string
}

type compiledRule struct {
	Rule
	endpoint *ir.Endpoint
	body     []fieldMatcher
	state    []typeCond
	consumed map[string]int
}

type fieldMatcher struct {
	field string
	m     bodyMatcher
}

type typeCond struct {
	typ string
	c   stateCond
}

func RulesPath(dataDir, name string) string {
	return filepath.Join(dataDir, "apis", name+".rules.json")
}

func LoadRuleSet(path string) (*RuleSet, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &RuleSet{}, nil
	}
	if err != nil {
		return nil, errfmt.Newf("cannot read the rules file", "check permissions on "+path, rulesDocs, "%v", err)
	}
	var rs RuleSet
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, errfmt.Newf("rules file is not valid JSON", "fix or remove "+path, rulesDocs, "%v", err)
	}
	return &rs, nil
}

func SaveRuleSet(path string, rs *RuleSet) error {
	rs.Version++
	for i := range rs.Rules {
		if rs.Rules[i].Version == 0 {
			rs.Rules[i].Version = rs.Version
		}
	}
	raw, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errfmt.Newf("cannot create the rules directory", "check permissions on "+filepath.Dir(path), rulesDocs, "%v", err)
	}
	if err := store.WriteFileAtomic(path, append(raw, '\n')); err != nil {
		return errfmt.Newf("cannot persist the rules file", "check permissions on "+path, rulesDocs, "%v", err)
	}
	return nil
}

var validRuleMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

func ValidateRules(def *ir.ApiDefinition, rs *RuleSet) error {
	_, err := compileRules(def, rs)
	return err
}

func compileRules(def *ir.ApiDefinition, rs *RuleSet) ([]compiledRule, error) {
	if rs == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []compiledRule
	for i := range rs.Rules {
		r := rs.Rules[i]
		refuse := func(why, fix string) error {
			return errfmt.New("rule "+r.ID+" cannot be loaded", why, fix, rulesDocs)
		}
		if strings.TrimSpace(r.ID) == "" {
			return nil, errfmt.New("a rule has no id", "every rule needs an id so the trace and `rule drop` can name it", "give it an id", rulesDocs)
		}
		if seen[r.ID] {
			return nil, refuse("another rule already uses the id "+r.ID, "give each rule its own id")
		}
		seen[r.ID] = true
		method := strings.ToUpper(r.When.Method)
		if !validRuleMethods[method] {
			return nil, refuse(fmt.Sprintf("when.method %q is not GET, POST, PUT, PATCH or DELETE", r.When.Method), "set when.method to the operation's method")
		}
		endpoint := endpointFor(def, method, r.When.Path)
		if endpoint == nil {
			return nil, errfmt.New("rule "+r.ID+" names a route the spec does not declare",
				method+" "+r.When.Path+" is not an operation in this sandbox's spec, and a rule cannot fire on a route the sandbox would not serve",
				"use the method and path template of a declared operation", rulesDocs)
		}
		if r.Respond.Status < 100 || r.Respond.Status > 599 {
			return nil, refuse(fmt.Sprintf("respond.status %d is not an HTTP status", r.Respond.Status), "set respond.status between 100 and 599")
		}
		if len(r.Respond.Body) > 0 && r.Respond.Example != 0 {
			return nil, refuse("respond has both a body and an example", "keep either respond.body or respond.example")
		}
		if len(r.Respond.Body) > 0 && !json.Valid(r.Respond.Body) {
			return nil, refuse("respond.body is not valid JSON", "fix respond.body")
		}
		if r.Respond.Example != 0 {
			if _, ok := exampleFor(def, endpoint, r.Respond.Example); !ok {
				return nil, refuse(fmt.Sprintf("the spec declares no example for %d on %s %s", r.Respond.Example, method, r.When.Path), "add an example to the spec and re-import, or use respond.body")
			}
		}
		if r.Respond.Emit != "" && !eventDeclared(def, r.Respond.Emit) {
			return nil, refuse("event "+r.Respond.Emit+" is not declared in the spec, and pikopod never invents an event", "declare it under webhooks in the spec and re-import")
		}
		if ss := r.Respond.SetState; ss != nil && (ss.Resource == "" || ss.State == "") {
			return nil, refuse("respond.set_state needs both resource and state", "set respond.set_state.resource to a resource type and .state to the state to enter")
		}
		if !validProvenance(r.Provenance) {
			return nil, refuse(fmt.Sprintf("provenance %q is not manual, promoted:<fp> or imported:<fp>", r.Provenance), "set provenance to one of those")
		}
		switch r.When.Per {
		case "", "global", "idempotency-key", "resource":
		default:
			return nil, refuse(fmt.Sprintf("when.per %q is not global, idempotency-key or resource", r.When.Per), "set when.per to one of those")
		}
		if r.When.Times < 0 {
			return nil, refuse("when.times is negative", "set when.times to 0 (always) or a positive count")
		}
		c := compiledRule{Rule: r, endpoint: endpoint}
		c.When.Method = method
		for _, field := range sortedRawKeys(r.When.Body) {
			m, err := parseBodyMatcher(r.When.Body[field])
			if err != nil {
				return nil, refuse("when.body."+field+": "+err.Error(), "use exists, absent, exists_in_store, or {\"equals\": <value>}")
			}
			c.body = append(c.body, fieldMatcher{field: field, m: m})
		}
		for _, typ := range sortedRawKeys(r.When.State) {
			sc, err := parseStateCond(r.When.State[typ])
			if err != nil {
				return nil, refuse("when.state."+typ+": "+err.Error(), "use {\"state_is\": <state>} or {\"state_not\": <state>}")
			}
			c.state = append(c.state, typeCond{typ: typ, c: sc})
		}
		out = append(out, c)
	}
	return out, nil
}

func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func validProvenance(p string) bool {
	if p == "manual" {
		return true
	}
	for _, prefix := range []string{"promoted:", "imported:"} {
		if strings.HasPrefix(p, prefix) && len(p) > len(prefix) {
			return true
		}
	}
	return false
}

func endpointFor(def *ir.ApiDefinition, method, path string) *ir.Endpoint {
	for i := range def.Endpoints {
		ep := &def.Endpoints[i]
		if strings.ToUpper(ep.Method.Value) == method && ep.PathTemplate.Value == path {
			return ep
		}
	}
	return nil
}

func exampleFor(def *ir.ApiDefinition, endpoint *ir.Endpoint, status int) (any, bool) {
	resp := errorResponseDef(endpoint, status)
	if resp == nil {
		return nil, false
	}
	var best *ir.Example
	for i := range def.Examples {
		ex := &def.Examples[i]
		if ex.ForNodeID != resp.ID || ex.MediaType == nil || !strings.Contains(strings.ToLower(*ex.MediaType), "json") {
			continue
		}
		if best == nil || ex.ID < best.ID {
			best = ex
		}
	}
	if best == nil {
		return nil, false
	}
	return best.Value.Value, true
}

func eventDeclared(def *ir.ApiDefinition, event string) bool {
	for i := range def.Webhooks {
		if def.Webhooks[i].Event.Value == event {
			return true
		}
	}
	return false
}

func parseBodyMatcher(raw json.RawMessage) (bodyMatcher, error) {
	var keyword string
	if json.Unmarshal(raw, &keyword) == nil {
		switch keyword {
		case "exists", "absent", "exists_in_store":
			return bodyMatcher{kind: keyword}, nil
		}
		return bodyMatcher{}, fmt.Errorf("%q is not a matcher", keyword)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) != 1 {
		return bodyMatcher{}, fmt.Errorf("a matcher is a keyword or an object with one key")
	}
	for k, v := range obj {
		if k != "equals" {
			return bodyMatcher{}, fmt.Errorf("%q is not a matcher", k)
		}
		return bodyMatcher{kind: "equals", value: compactJSON(v)}, nil
	}
	return bodyMatcher{}, fmt.Errorf("empty matcher")
}

func parseStateCond(raw json.RawMessage) (stateCond, error) {
	var obj map[string]string
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) != 1 {
		return stateCond{}, fmt.Errorf("a state condition is an object with one key and a string value")
	}
	for k, v := range obj {
		if k != "state_is" && k != "state_not" {
			return stateCond{}, fmt.Errorf("%q is not a state condition", k)
		}
		if v == "" {
			return stateCond{}, fmt.Errorf("%s needs a state", k)
		}
		return stateCond{kind: k, value: v}, nil
	}
	return stateCond{}, fmt.Errorf("empty condition")
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return raw
	}
	return json.RawMessage(buf.Bytes())
}

func (r *compiledRule) summary() string {
	parts := []string{r.When.Method + " " + r.When.Path}
	var conds []string
	for _, fm := range r.body {
		if fm.m.kind == "equals" {
			conds = append(conds, "body."+fm.field+" equals "+string(fm.m.value))
			continue
		}
		conds = append(conds, "body."+fm.field+" "+fm.m.kind)
	}
	for _, tc := range r.state {
		conds = append(conds, "state "+tc.typ+" "+tc.c.kind+" "+tc.c.value)
	}
	if r.When.Times > 0 {
		w := "first " + strconv.Itoa(r.When.Times)
		if r.When.Per != "" && r.When.Per != "global" {
			w += " per " + r.When.Per
		}
		conds = append(conds, w)
	}
	if len(conds) > 0 {
		parts = append(parts, "when "+strings.Join(conds, ", "))
	}
	return strings.Join(parts, " ")
}

func (e *Engine) Rules() []Rule {
	out := make([]Rule, 0, len(e.rules))
	for i := range e.rules {
		out = append(out, e.rules[i].Rule)
	}
	return out
}

func (e *Engine) applyRules(result *matchResult, req *ingressRequest, innerPath string) *RawResponse {
	if len(e.rules) == 0 {
		return nil
	}
	op := deriveOperation(result.endpoint, result.pathParams)
	e.rulesMu.Lock()
	defer e.rulesMu.Unlock()
	for i := range e.rules {
		r := &e.rules[i]
		if r.endpoint != result.endpoint {
			continue
		}
		if !e.ruleConditionsHold(r, req, &op) {
			continue
		}
		if r.When.Times > 0 && !consumeWindow(&r.consumed, r.When.Times, windowKey(r.When.Per, req, innerPath)) {
			continue
		}
		e.tracef("rules", "rule %s fired: %s", r.ID, r.summary())
		return e.ruleResponse(r, &op)
	}
	e.tracef("rules", "no rule matched")
	return nil
}

func consumeWindow(consumed *map[string]int, times int, key string) bool {
	if *consumed == nil {
		*consumed = map[string]int{}
	}
	count, seen := (*consumed)[key]
	if count >= times {
		return false
	}
	if !seen && len(*consumed) >= maxFaultWindowKeys {
		return false
	}
	(*consumed)[key] = count + 1
	return true
}

func (e *Engine) ruleConditionsHold(r *compiledRule, req *ingressRequest, op *operation) bool {
	for _, fm := range r.body {
		value, present := bodyField(req, fm.field)
		switch fm.m.kind {
		case "exists":
			if !present {
				return false
			}
		case "absent":
			if present {
				return false
			}
		case "equals":
			if !present || !bytes.Equal(canonical(value), fm.m.value) {
				return false
			}
		case "exists_in_store":
			if !present || !e.attributeStored(op.typ, fm.field, canonical(value)) {
				return false
			}
		}
	}
	for _, tc := range r.state {
		if !e.stateHolds(tc, op) {
			return false
		}
	}
	return true
}

func bodyField(req *ingressRequest, field string) (any, bool) {
	obj, ok := req.bodyValue.(*JSONObject)
	if !ok {
		return nil, false
	}
	return obj.Get(field)
}

func canonical(v any) json.RawMessage {
	raw, err := marshalJSValue(v)
	if err != nil {
		return nil
	}
	return compactJSON(raw)
}

func (e *Engine) attributeStored(typ, field string, want json.RawMessage) bool {
	var cursor *string
	for {
		page, err := e.store.List(e.id, typ, 1000, cursor)
		if err != nil {
			return false
		}
		for _, item := range page.Items {
			var attrs map[string]json.RawMessage
			if json.Unmarshal(item.Attributes, &attrs) != nil {
				continue
			}
			if got, ok := attrs[field]; ok && bytes.Equal(compactJSON(got), want) {
				return true
			}
		}
		if page.NextCursorKey == nil {
			return false
		}
		cursor = page.NextCursorKey
	}
}

func (e *Engine) stateHolds(tc typeCond, op *operation) bool {
	if op.typ == tc.typ && op.key != nil {
		res, err := e.store.GetOne(e.id, tc.typ, *op.key)
		if err != nil {
			return false
		}
		return stateMatches(tc.c, res.State)
	}
	any, someMatch := false, false
	var cursor *string
	for {
		page, err := e.store.List(e.id, tc.typ, 1000, cursor)
		if err != nil {
			return false
		}
		for _, item := range page.Items {
			any = true
			if item.State != nil && *item.State == tc.c.value {
				someMatch = true
			}
		}
		if page.NextCursorKey == nil {
			break
		}
		cursor = page.NextCursorKey
	}
	if tc.c.kind == "state_is" {
		return someMatch
	}
	return any && !someMatch
}

func stateMatches(c stateCond, state *string) bool {
	current := ""
	if state != nil {
		current = *state
	}
	if c.kind == "state_is" {
		return current == c.value
	}
	return current != c.value
}

func (e *Engine) ruleResponse(r *compiledRule, op *operation) *RawResponse {
	resp := &RawResponse{Status: r.Respond.Status, Headers: map[string]string{}}
	switch {
	case len(r.Respond.Body) > 0:
		resp.Body = append([]byte(nil), r.Respond.Body...)
		resp.Headers["content-type"] = jsonContentType
	case r.Respond.Example != 0:
		if value, ok := exampleFor(e.def, r.endpoint, r.Respond.Example); ok {
			resp = jsonResponse(r.Respond.Status, value, nil)
		}
	}
	for k, v := range r.Respond.Headers {
		resp.Headers[strings.ToLower(k)] = v
	}
	resp.Headers[RuleHeader] = r.ID
	if ss := r.Respond.SetState; ss != nil {
		if op.typ == ss.Resource && op.key != nil {
			if res, err := e.store.GetOne(e.id, ss.Resource, *op.key); err == nil {
				state := ss.State
				if _, err := e.store.Update(e.id, ss.Resource, *op.key, res.Attributes, nil, &state, true, e.virtualClockMs); err == nil {
					e.tracef("rules", "rule %s set %s/%s state to %s", r.ID, ss.Resource, *op.key, ss.State)
				}
			} else {
				e.tracef("rules", "rule %s set_state skipped: no %s resource %s", r.ID, ss.Resource, *op.key)
			}
		} else {
			e.tracef("rules", "rule %s set_state skipped: the request addresses no %s resource", r.ID, ss.Resource)
		}
	}
	if r.Respond.Emit != "" {
		if err := e.EmitWebhook(r.Respond.Emit, nil); err != nil {
			e.tracef("rules", "rule %s could not emit %s: %v", r.ID, r.Respond.Emit, err)
		} else {
			e.tracef("rules", "rule %s emitted %s", r.ID, r.Respond.Emit)
		}
	}
	return resp
}
