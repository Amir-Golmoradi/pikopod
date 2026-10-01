package truth

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/ir"
	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/sandbox"
	"github.com/pikopod/pikopod/internal/volatile"
)

type Options struct {
	Seed     string
	Volatile []string
	Limit    int
}

type Leaf struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Comparison struct {
	Compared   int    `json:"compared"`
	Reproduced int    `json:"reproduced"`
	ShapeOnly  int    `json:"shapeOnly"`
	Wrong      []Leaf `json:"wrong,omitempty"`
}

type EndpointScore struct {
	Method     string  `json:"method"`
	Template   string  `json:"template"`
	Responses  int     `json:"responses"`
	Compared   int     `json:"compared"`
	Reproduced int     `json:"reproduced"`
	ShapeOnly  int     `json:"shapeOnly"`
	Unanswered int     `json:"unanswered"`
	Percent    float64 `json:"percent"`
}

type WorstField struct {
	Path   string `json:"path"`
	Count  int    `json:"count"`
	Source string `json:"source"`
}

type Report struct {
	Responses   int             `json:"responses"`
	Endpoints   int             `json:"endpoints"`
	Compared    int             `json:"compared"`
	Reproduced  int             `json:"reproduced"`
	ShapeOnly   int             `json:"shapeOnly"`
	Percent     float64         `json:"percent"`
	PerEndpoint []EndpointScore `json:"perEndpoint"`
	Worst       []WorstField    `json:"worst"`
	Unanswered  []string        `json:"unanswered"`
}

func flatten(node any, path string, out map[string]any) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			flatten(child, path+"/"+escapePointer(k), out)
		}
	case []any:
		for i, child := range v {
			flatten(child, path+"/"+strconv.Itoa(i), out)
		}
	default:
		out[path] = v
	}
}

func escapePointer(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func kind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, json.Number, int, int64:
		return "number"
	}
	return "other"
}

func sameValue(a, b any) bool {
	if kind(a) == "number" {
		fa, oka := asFloat(a)
		fb, okb := asFloat(b)
		return oka && okb && fa == fb
	}
	return a == b
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func Compare(recorded, sandboxBody any, recordedStatus, sandboxStatus int, redacted map[string]bool, isVolatile func(path string) bool) Comparison {
	var c Comparison
	c.Compared++
	if recordedStatus == sandboxStatus {
		c.Reproduced++
	} else {
		c.Wrong = append(c.Wrong, Leaf{Path: "/", Reason: "status"})
	}
	rec, sbx := map[string]any{}, map[string]any{}
	flatten(recorded, "", rec)
	flatten(sandboxBody, "", sbx)
	if _, scalar := rec[""]; scalar && recorded == nil {
		delete(rec, "")
	}
	if _, scalar := sbx[""]; scalar && sandboxBody == nil {
		delete(sbx, "")
	}
	paths := make([]string, 0, len(rec))
	for p := range rec {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		c.Compared++
		got, present := sbx[p]
		if !present {
			c.Wrong = append(c.Wrong, Leaf{Path: p, Reason: "missing"})
			continue
		}
		if kind(got) != kind(rec[p]) {
			c.Wrong = append(c.Wrong, Leaf{Path: p, Reason: "type"})
			continue
		}
		if redacted[p] || (isVolatile != nil && isVolatile(p)) {
			c.Reproduced++
			c.ShapeOnly++
			continue
		}
		if sameValue(rec[p], got) {
			c.Reproduced++
			continue
		}
		c.Wrong = append(c.Wrong, Leaf{Path: p, Reason: "value"})
	}
	extras := make([]string, 0)
	for p := range sbx {
		if _, has := rec[p]; !has {
			extras = append(extras, p)
		}
	}
	sort.Strings(extras)
	for _, p := range extras {
		c.Compared++
		if redacted[p] {
			c.Reproduced++
			c.ShapeOnly++
			continue
		}
		c.Wrong = append(c.Wrong, Leaf{Path: p, Reason: "extra"})
	}
	return c
}

func redactedPointers(rec *proxy.Record) map[string]bool {
	out := map[string]bool{}
	for _, r := range rec.Redacted {
		if r.Section == "resp_body" {
			out[r.Pointer] = true
		}
	}
	return out
}

func templateFor(def *ir.ApiDefinition, method, path string) (string, bool) {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	want := strings.Split(strings.Trim(path, "/"), "/")
	for i := range def.Endpoints {
		ep := &def.Endpoints[i]
		if !strings.EqualFold(ep.Method.Value, method) {
			continue
		}
		have := strings.Split(strings.Trim(ep.PathTemplate.Value, "/"), "/")
		if len(have) != len(want) {
			continue
		}
		ok := true
		for j := range have {
			if !strings.HasPrefix(have[j], "{") && have[j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return ep.PathTemplate.Value, true
		}
	}
	return path, false
}

func Score(def *ir.ApiDefinition, records []*proxy.Record, opts Options) (*Report, error) {
	store, err := sandbox.OpenMemoryStore()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	eng, err := sandbox.NewEngine(def, sandbox.Config{ID: "sbx_truth", Seed: opts.Seed, Mode: "deterministic"}, store)
	if err != nil {
		return nil, err
	}
	matcher, _, err := volatile.CompileSets(opts.Volatile, volatile.ResponseFieldNames())
	if err != nil {
		return nil, err
	}
	isVolatile := func(path string) bool {
		_, hit := matcher.Match(strings.TrimPrefix(path, "/"))
		return hit
	}
	sources := map[string]string{}
	eng.SetTrace(func(stage, message string) {
		if stage != "synth" {
			return
		}
		field, source, ok := strings.Cut(message, " ← ")
		if ok {
			sources[strings.TrimSpace(field)] = strings.TrimSpace(source)
		}
	})
	if opts.Limit > 0 && len(records) > opts.Limit {
		records = records[len(records)-opts.Limit:]
	}
	report := &Report{}
	perEndpoint := map[string]*EndpointScore{}
	var keys []string
	wrongCounts := map[string]int{}
	wrongSources := map[string]string{}
	unanswered := map[string]bool{}
	hname, hvalue, hasAuth := eng.AuthHeader()
	for _, rec := range records {
		if rec.RespKind != "json" || rec.RespBody == nil {
			continue
		}
		template, _ := templateFor(def, rec.Method, rec.Path)
		key := strings.ToUpper(rec.Method) + " " + template
		es, known := perEndpoint[key]
		if !known {
			es = &EndpointScore{Method: strings.ToUpper(rec.Method), Template: template}
			perEndpoint[key] = es
			keys = append(keys, key)
		}
		body := ""
		if rec.ReqKind == "json" && rec.ReqBody != nil {
			raw, _ := json.Marshal(rec.ReqBody)
			body = string(raw)
		}
		req := httptest.NewRequest(strings.ToUpper(rec.Method), rec.Path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("content-type", "application/json")
		}
		if hasAuth {
			req.Header.Set(hname, hvalue)
		}
		for k := range sources {
			delete(sources, k)
		}
		w := httptest.NewRecorder()
		eng.ServeHTTP(w, req)
		var sandboxBody any
		if w.Body.Len() > 0 {
			dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
			dec.UseNumber()
			if dec.Decode(&sandboxBody) != nil {
				sandboxBody = nil
			}
		}
		notServed := w.Code == 404 && rec.Status != 404
		if notServed {
			es.Unanswered++
			unanswered[key] = true
		}
		c := Compare(rec.RespBody, sandboxBody, rec.Status, w.Code, redactedPointers(rec), isVolatile)
		es.Responses++
		es.Compared += c.Compared
		es.Reproduced += c.Reproduced
		es.ShapeOnly += c.ShapeOnly
		for _, wrong := range c.Wrong {
			if wrong.Reason == "status" || notServed {
				continue
			}
			wrongCounts[wrong.Path]++
			field := wrong.Path[strings.LastIndex(wrong.Path, "/")+1:]
			if src, ok := sources[field]; ok {
				wrongSources[wrong.Path] = src
			} else if _, known := wrongSources[wrong.Path]; !known {
				if wrong.Reason == "missing" {
					wrongSources[wrong.Path] = "not served"
				} else {
					wrongSources[wrong.Path] = wrong.Reason
				}
			}
		}
		report.Responses++
		report.Compared += c.Compared
		report.Reproduced += c.Reproduced
		report.ShapeOnly += c.ShapeOnly
	}
	sort.Strings(keys)
	for _, k := range keys {
		es := perEndpoint[k]
		es.Percent = percent(es.Reproduced, es.Compared)
		report.PerEndpoint = append(report.PerEndpoint, *es)
	}
	report.Endpoints = len(keys)
	report.Percent = percent(report.Reproduced, report.Compared)
	for p, n := range wrongCounts {
		report.Worst = append(report.Worst, WorstField{Path: p, Count: n, Source: wrongSources[p]})
	}
	sort.Slice(report.Worst, func(i, j int) bool {
		if report.Worst[i].Count != report.Worst[j].Count {
			return report.Worst[i].Count > report.Worst[j].Count
		}
		return report.Worst[i].Path < report.Worst[j].Path
	})
	if len(report.Worst) > 3 {
		report.Worst = report.Worst[:3]
	}
	for k := range unanswered {
		report.Unanswered = append(report.Unanswered, k)
	}
	sort.Strings(report.Unanswered)
	return report, nil
}

func percent(reproduced, compared int) float64 {
	if compared == 0 {
		return 0
	}
	return math.Round(float64(reproduced) / float64(compared) * 100)
}

func (r *Report) Text() string {
	var b strings.Builder
	for _, es := range r.PerEndpoint {
		line := fmt.Sprintf("%-44s %3.0f%%  (%d leaves over %d response(s)", es.Method+" "+es.Template, es.Percent, es.Compared, es.Responses)
		if es.ShapeOnly > 0 {
			line += fmt.Sprintf("; %d shape-only", es.ShapeOnly)
		}
		if es.Unanswered > 0 {
			line += fmt.Sprintf("; sandbox answered 404 for %d", es.Unanswered)
		}
		b.WriteString(line + ")\n")
	}
	if len(r.Worst) > 0 {
		b.WriteString("worst:\n")
		for _, w := range r.Worst {
			field := w.Path[strings.LastIndex(w.Path, "/")+1:]
			b.WriteString(fmt.Sprintf("  %-28s %3d×  %s ← %s\n", w.Path, w.Count, field, w.Source))
		}
	}
	b.WriteString(fmt.Sprintf("truthfulness: %.0f%% over %d recorded responses on %d endpoints", r.Percent, r.Responses, r.Endpoints))
	if r.ShapeOnly > 0 {
		b.WriteString(fmt.Sprintf(" (%d leaves shape-only)", r.ShapeOnly))
	}
	b.WriteString("\n")
	return b.String()
}

func LoadRecordings(dataDir, upstream string) ([]*proxy.Record, error) {
	var out []*proxy.Record
	base := filepath.Join(dataDir, "recordings", upstream+".ndjson")
	for _, path := range []string{base + ".1", base} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var rec proxy.Record
			dec := json.NewDecoder(bytes.NewReader(line))
			dec.UseNumber()
			if dec.Decode(&rec) == nil {
				out = append(out, &rec)
			}
		}
		f.Close()
	}
	return out, nil
}

func NoRecordings(upstream, agentURL string) error {
	return errfmt.New("no recordings for "+upstream,
		"the truthfulness number needs responses the real provider sent, and none are on disk",
		"point your app at the agent ("+agentURL+"/"+upstream+") and run your tests once, then retry",
		"docs/config-reference.md#data_dir")
}
