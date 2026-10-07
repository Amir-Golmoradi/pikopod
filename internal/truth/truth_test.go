package truth

import (
	"encoding/json"
	"github.com/pikopod/pikopod/internal/replay"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/importer"
	"github.com/pikopod/pikopod/internal/proxy"
)

func leafReasons(c Comparison) string {
	var out []string
	for _, w := range c.Wrong {
		out = append(out, w.Path+":"+w.Reason)
	}
	return strings.Join(out, " ")
}

func TestCompareCountsEveryKindOfDifference(t *testing.T) {
	recorded := map[string]any{"id": "ch_1", "amount": float64(5000), "currency": "usd", "status": "success", "created": "2025-01-01T00:00:00Z"}
	sandbox := map[string]any{"id": "charges_1", "amount": "5000", "currency": "usd", "extra": true}
	c := Compare(recorded, sandbox, 201, 201, map[string]bool{"/id": true}, func(string) bool { return false })
	if c.Compared != 7 || c.Reproduced != 3 || c.ShapeOnly != 1 {
		t.Fatalf("compared=%d reproduced=%d shapeOnly=%d: %s", c.Compared, c.Reproduced, c.ShapeOnly, leafReasons(c))
	}
	if got := leafReasons(c); got != "/amount:type /created:missing /status:missing /extra:extra" {
		t.Fatalf("wrong leaves: %s", got)
	}
	dropped := Compare(map[string]any{"id": "ch_1"}, map[string]any{"id": "ch_1", "currency": "USD"}, 200, 200, map[string]bool{"/currency": true}, nil)
	if dropped.Compared != 3 || dropped.Reproduced != 3 || dropped.ShapeOnly != 1 || len(dropped.Wrong) != 0 {
		t.Fatalf("a field the recorder dropped is shape-only when the sandbox serves it, never extra: %+v", dropped)
	}
}

func TestCompareScoresStatusAndVolatileFields(t *testing.T) {
	c := Compare(map[string]any{"ts": "a", "ok": true}, map[string]any{"ts": "b", "ok": true}, 200, 503, nil, func(path string) bool { return path == "/ts" })
	if c.Compared != 3 || c.Reproduced != 2 || c.ShapeOnly != 1 || leafReasons(c) != "/:status" {
		t.Fatalf("status must be one leaf and a volatile field shape-only: compared=%d reproduced=%d shapeOnly=%d %s", c.Compared, c.Reproduced, c.ShapeOnly, leafReasons(c))
	}
	nested := Compare(map[string]any{"data": map[string]any{"items": []any{map[string]any{"n": float64(1)}}}}, map[string]any{"data": map[string]any{"items": []any{map[string]any{"n": float64(2)}}}}, 200, 200, nil, func(string) bool { return false })
	if nested.Compared != 2 || nested.Reproduced != 1 || leafReasons(nested) != "/data/items/0/n:value" {
		t.Fatalf("nested leaves use JSON pointers: %+v %s", nested, leafReasons(nested))
	}
}

const thingsSpec = `{
  "openapi": "3.1.0",
  "info": {"title": "Things", "version": "1.0.0"},
  "paths": {
    "/things": {"post": {
      "requestBody": {"content": {"application/json": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}}},
      "responses": {"201": {"description": "created", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}}}
    }},
    "/things/{id}": {
      "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
      "get": {"responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}}}}
    }
  },
  "components": {"schemas": {"Thing": {"type": "object", "properties": {
    "id": {"type": "string"}, "name": {"type": "string"}, "status": {"type": "string", "enum": ["active"]}, "amount": {"type": "integer"}
  }}}}
}`

func TestScoreReplaysRecordingsAgainstAFreshEngine(t *testing.T) {
	def, err := importer.NormalizeOpenAPI([]byte(thingsSpec))
	if err != nil {
		t.Fatal(err)
	}
	records := []*proxy.Record{
		{TS: time.Now(), Upstream: "things", Method: "POST", Path: "/things", Status: 201, ReqKind: "json", ReqBody: map[string]any{"name": "x"},
			RespKind: "json", RespBody: map[string]any{"id": "t1", "name": "x", "status": "active", "amount": float64(5)},
			Redacted: []proxy.SectionRedaction{{Section: "resp_body", Pointer: "/id", Mode: "TOKENIZE"}}},
		{TS: time.Now(), Upstream: "things", Method: "GET", Path: "/things/t1", Status: 200,
			RespKind: "json", RespBody: map[string]any{"id": "t1", "name": "x", "status": "active", "amount": float64(5)}},
	}
	report, err := Score(def, records, Options{Seed: "truth-1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Responses != 2 || len(report.PerEndpoint) != 2 {
		t.Fatalf("responses=%d endpoints=%d", report.Responses, len(report.PerEndpoint))
	}
	if len(report.Unanswered) != 1 || report.Unanswered[0] != "GET /things/{id}" {
		t.Fatalf("a read of a resource the sandbox never created scores zero and is listed: %v", report.Unanswered)
	}
	var create *EndpointScore
	for i := range report.PerEndpoint {
		if report.PerEndpoint[i].Method == "POST" {
			create = &report.PerEndpoint[i]
		}
	}
	if create == nil || create.Compared != 5 || create.Reproduced != 4 || create.ShapeOnly != 1 {
		t.Fatalf("create: status, name echo, enum status and tokenized id reproduce; amount does not: %+v", create)
	}
	if len(report.Worst) == 0 || report.Worst[0].Path != "/amount" || !strings.HasPrefix(report.Worst[0].Source, "convention") {
		t.Fatalf("the worst list names the field and the sandbox's source for it: %+v", report.Worst)
	}
	text := report.Text()
	for _, want := range []string{"POST /things", "GET /things/{id}", "0%", "truthfulness:", "over 2 recorded responses on 2 endpoints", "/amount", "← convention"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
}

func TestAChainedReadScoresAgainstTheStoredResource(t *testing.T) {
	def, err := importer.NormalizeOpenAPI([]byte(thingsSpec))
	if err != nil {
		t.Fatal(err)
	}
	create := &proxy.Record{TS: time.Now(), Upstream: "things", Method: "POST", Path: "/things", Status: 201, ReqKind: "json", ReqBody: map[string]any{"name": "x"},
		RespKind: "json", RespBody: map[string]any{"id": "t1", "name": "x", "status": "active", "amount": float64(5)},
		Redacted: []proxy.SectionRedaction{{Section: "resp_body", Pointer: "/id", Mode: "TOKENIZE"}}}
	read := &proxy.Record{TS: time.Now(), Upstream: "things", Method: "GET", Path: "/things/t1", Status: 200,
		RespKind: "json", RespBody: map[string]any{"id": "t1", "name": "x", "status": "active", "amount": float64(5)}}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "recordings"), 0o700)
	raw, _ := json.Marshal(create)
	os.WriteFile(filepath.Join(dir, "recordings", "things.ndjson"), append(raw, '\n'), 0o600)
	set, err := replay.Load(dir, "things", nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Score(def, []*proxy.Record{create, read}, Options{Seed: "truth-2", Recordings: set, RecordingsMode: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unanswered) != 0 {
		t.Fatalf("the read of the id the recorded create returned lands on the stored resource, not 404: %v\n%s", report.Unanswered, report.Text())
	}
	for _, es := range report.PerEndpoint {
		if es.Method == "GET" && (es.Reproduced == 0 || es.Compared != 5) {
			t.Fatalf("the chained read scores against the stored resource: %+v", es)
		}
	}
}
