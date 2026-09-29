package docimport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pikopod/pikopod/internal/importer"
	"github.com/pikopod/pikopod/internal/scenario/nl"
)

func llmsIndex(links ...string) []byte {
	var b strings.Builder
	for i, l := range links {
		fmt.Fprintf(&b, "- [Page %d](%s)\n", i, l)
	}
	return []byte(b.String())
}

func TestLlmsTxtKeepsEveryReferencePage(t *testing.T) {
	var links []string
	for i := 0; i < 70; i++ {
		links = append(links, fmt.Sprintf("https://docs.x.test/guides/page-%d", i))
	}
	for i := 0; i < 4; i++ {
		links = append(links, fmt.Sprintf("https://docs.x.test/wallet/api-reference/op-%d.md", i))
	}
	links = append(links, "https://docs.x.test/webhooks/events")
	pages := map[string][]byte{"https://docs.x.test/llms.txt": llmsIndex(links...)}
	groups := llmsTxtGroups("https://docs.x.test/intro", fetcherFor(pages))
	if len(groups) != 3 {
		t.Fatalf("groups: %d", len(groups))
	}
	if len(groups[0]) != 1 || len(groups[1]) != 4 {
		t.Fatalf("every webhook and reference page must survive the index cap: hooks=%d refs=%d", len(groups[0]), len(groups[1]))
	}
	if len(groups[2]) != maxContextPages {
		t.Fatalf("context pages are the only ones capped, got %d want %d", len(groups[2]), maxContextPages)
	}
}

type captured struct {
	mu       sync.Mutex
	requests []string
}

func fakeModel(t *testing.T, cap *captured, answer func(userPayload string) string) *nl.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.Unmarshal(raw, &req)
		system, user := "", ""
		for _, m := range req.Messages {
			if m.Role == "system" {
				system += m.Content
			} else {
				user += m.Content
			}
		}
		if cap != nil {
			cap.mu.Lock()
			cap.requests = append(cap.requests, system+"\n---\n"+user)
			cap.mu.Unlock()
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": answer(user)}}}})
	}))
	t.Cleanup(srv.Close)
	llm := nl.NewClient("test-key", "test-model")
	llm.BaseURL = srv.URL
	return llm
}

const emptySpec = `{"openapi":"3.0.0","info":{"title":"api","version":"1"},"paths":{}}`

func TestPayloadTagsPagesAndInstructionNamesClasses(t *testing.T) {
	pages := map[string][]byte{
		"https://docs.x.test/llms.txt":              llmsIndex("https://docs.x.test/api-reference/create", "https://docs.x.test/guides/limits", "https://docs.x.test/webhooks/events"),
		"https://docs.x.test/api-reference/create":  []byte(strings.Repeat("POST /things creates a thing. ", 20)),
		"https://docs.x.test/guides/limits":         []byte(strings.Repeat("amounts are capped at 5000000. ", 20)),
		"https://docs.x.test/webhooks/events":       []byte(strings.Repeat("thing.created fires after create. ", 20)),
		"https://docs.x.test/api-reference/create2": nil,
	}
	cap := &captured{}
	llm := fakeModel(t, cap, func(string) string { return emptySpec })
	if _, err := FromDocsURL("https://docs.x.test/intro", []byte("<html><body>x</body></html>"), fetcherFor(pages), llm); err != nil {
		t.Fatal(err)
	}
	if len(cap.requests) == 0 {
		t.Fatal("no request reached the model")
	}
	all := strings.Join(cap.requests, "\n")
	for _, want := range []string{`"kind":"reference"`, `"kind":"context"`, `"kind":"webhook"`} {
		if !strings.Contains(all, want) {
			t.Errorf("payload must tag pages with %s:\n%s", want, all[:min(len(all), 1500)])
		}
	}
	system := strings.SplitN(cap.requests[0], "\n---\n", 2)[0]
	for _, want := range []string{"reference", "context", "webhook", "apiKey", "header", "example", "servers"} {
		if !strings.Contains(system, want) {
			t.Errorf("instruction must mention %q", want)
		}
	}
}

func TestPathsComeOnlyFromReferenceBatches(t *testing.T) {
	big := func(s string) []byte { return []byte(strings.Repeat(s, 3200)) }
	pages := map[string][]byte{
		"https://docs.x.test/llms.txt":             llmsIndex("https://docs.x.test/api-reference/create", "https://docs.x.test/guides/channels"),
		"https://docs.x.test/api-reference/create": big("POST /things. "),
		"https://docs.x.test/guides/channels":      big("channels list. "),
	}
	answer := func(user string) string {
		if strings.Contains(user, "api-reference/create") {
			return `{"openapi":"3.0.0","info":{"title":"api","version":"1"},"paths":{"/things":{"post":{"responses":{"201":{"description":"c"}}}}},"servers":[{"url":"https://api.x.test/v1"}]}`
		}
		return `{"openapi":"3.0.0","info":{"title":"api","version":"1"},"paths":{"/invented":{"post":{"responses":{"200":{"description":"x"}}}}},"webhooks":{"thing.created":{"post":{"responses":{"200":{"description":"ack"}}}}},"components":{"schemas":{"Channel":{"type":"string","enum":["A","B"]}}}}`
	}
	res, err := FromDocsURL("https://docs.x.test/intro", []byte("<html><body>x</body></html>"), fetcherFor(pages), fakeModel(t, nil, answer))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	json.Unmarshal(res.Spec, &doc)
	paths, _ := doc["paths"].(map[string]any)
	if _, has := paths["/things"]; !has {
		t.Fatalf("the reference batch's path must be kept: %v", keysOf(paths))
	}
	if _, has := paths["/invented"]; has {
		t.Fatalf("a path described only by a context page must be dropped: %v", keysOf(paths))
	}
	if _, has := dig(doc, "webhooks")["thing.created"]; !has {
		t.Fatalf("webhooks from context batches survive: %v", doc["webhooks"])
	}
	if _, has := dig(doc, "components", "schemas")["Channel"]; !has {
		t.Fatalf("components from context batches survive: %v", doc["components"])
	}
	if len(res.Dropped) != 1 || res.Dropped[0] != "POST /invented" {
		t.Fatalf("the dropped endpoint must be named: %v", res.Dropped)
	}

	contextOnly := map[string][]byte{
		"https://docs.x.test/llms.txt": llmsIndex("https://docs.x.test/guides/a", "https://docs.x.test/guides/b"),
		"https://docs.x.test/guides/a": big("GET /a. "),
		"https://docs.x.test/guides/b": big("GET /b. "),
	}
	res, err = FromDocsURL("https://docs.x.test/intro", []byte("<html><body>x</body></html>"), fetcherFor(contextOnly), fakeModel(t, nil, func(user string) string {
		if strings.Contains(user, "guides/a") {
			return `{"openapi":"3.0.0","info":{"title":"api","version":"1"},"paths":{"/a":{"get":{"responses":{"200":{"description":"a"}}}}}}`
		}
		return `{"openapi":"3.0.0","info":{"title":"api","version":"1"},"paths":{"/b":{"get":{"responses":{"200":{"description":"b"}}}}}}`
	}))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(res.Spec, &doc)
	paths, _ = doc["paths"].(map[string]any)
	if len(paths) != 2 {
		t.Fatalf("with no reference pages every page may define endpoints: %v", keysOf(paths))
	}
}

func TestServersNeverPointAtTheDocsHost(t *testing.T) {
	prose := []byte(`<html><body><h1>API</h1><p>GET /ping.</p></body></html>`)
	docsHost := `{"openapi":"3.0.0","info":{"title":"api","version":"1"},"paths":{"/ping":{"get":{"responses":{"200":{"description":"ok"}}}}},"servers":[{"url":"https://docs.x.test/guide"}]}`
	res, err := FromDocsURL("https://docs.x.test/guide", prose, fetcherFor(nil), fakeModel(t, nil, func(string) string { return docsHost }))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	json.Unmarshal(res.Spec, &doc)
	if _, has := doc["servers"]; has {
		t.Fatalf("a server on the documentation host must be dropped: %v", doc["servers"])
	}
	realHost := strings.Replace(docsHost, "https://docs.x.test/guide", "https://api.x.test/v2", 1)
	res, err = FromDocsURL("https://docs.x.test/guide", prose, fetcherFor(nil), fakeModel(t, nil, func(string) string { return realHost }))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(res.Spec, &doc)
	servers, _ := doc["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["url"] != "https://api.x.test/v2" {
		t.Fatalf("a real base URL must be kept: %v", doc["servers"])
	}
}

func TestExamplesSurviveToTheIR(t *testing.T) {
	prose := []byte(`<html><body><h1>API</h1><p>POST /accounts.</p></body></html>`)
	spec := `{"openapi":"3.0.0","info":{"title":"api","version":"1"},"paths":{"/accounts":{"post":{
	  "requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string","example":"COMPANY"}}},"example":{"name":"COMPANY"}}}},
	  "responses":{"201":{"description":"created","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"},"bankName":{"type":"string","example":"FIRST BANK"}}},"example":{"id":"acc_1","bankName":"FIRST BANK"}}}}}}}}}`
	res, err := FromDocsURL("https://docs.x.test/guide", prose, fetcherFor(nil), fakeModel(t, nil, func(string) string { return spec }))
	if err != nil {
		t.Fatal(err)
	}
	def, err := importer.NormalizeOpenAPI(res.Spec)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(def.Examples) < 2 {
		t.Fatalf("media-type examples must reach the IR, got %d", len(def.Examples))
	}
	found := false
	for i := range def.Endpoints {
		for _, r := range def.Endpoints[i].Responses {
			for _, c := range r.Content {
				for _, p := range c.Schema.Properties {
					for _, k := range p.Schema.Constraints {
						if k.Key == "example" && k.Value.Value == "FIRST BANK" {
							found = true
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("property-level example must reach the IR as a constraint")
	}
}

func TestPathItemsKeepOnlyOperations(t *testing.T) {
	doc := map[string]any{"paths": map[string]any{
		"/wallet/balance": map[string]any{"get": map[string]any{"responses": map[string]any{}}, "/WALLET/STATEMENT": map[string]any{}, "COMPONENTS": map[string]any{}, "parameters": []any{}},
		"COMPONENTS":      map[string]any{"schemas": map[string]any{}},
	}}
	cleanPathKeys(doc)
	paths := doc["paths"].(map[string]any)
	if len(paths) != 1 {
		t.Fatalf("keys that are not paths must go: %v", keysOf(paths))
	}
	item := paths["/wallet/balance"].(map[string]any)
	if len(item) != 2 || item["get"] == nil || item["parameters"] == nil {
		t.Fatalf("a path item keeps only operations and item-level keys: %v", keysOf(item))
	}
}

func refPage(path string) []byte {
	return []byte(strings.Repeat("POST "+path+" creates a thing. ", 30))
}

func opFor(path, source string) string {
	return `"` + path + `":{"post":{"x-pikopod-source":"` + source + `","responses":{"201":{"description":"c"}}}}`
}

func specWith(ops ...string) string {
	return `{"openapi":"3.0.0","info":{"title":"api","version":"1"},"paths":{` + strings.Join(ops, ",") + `}}`
}

func TestEmptyReferencePagesAreRetriedAlone(t *testing.T) {
	a, b, c := "https://docs.x.test/api-reference/a", "https://docs.x.test/api-reference/b", "https://docs.x.test/api-reference/c"
	pages := map[string][]byte{
		"https://docs.x.test/llms.txt": llmsIndex(a, b, c),
		a:                              refPage("/a"), b: refPage("/b"), c: refPage("/c"),
	}
	cap := &captured{}
	llm := fakeModel(t, cap, func(user string) string {
		if strings.Contains(user, a) && strings.Contains(user, b) {
			return specWith(opFor("/a", a), opFor("/b", b))
		}
		if strings.Contains(user, c) && !strings.Contains(user, a) {
			return specWith(opFor("/c", c))
		}
		return emptySpec
	})
	res, err := FromDocsURL("https://docs.x.test/intro", []byte("<html><body>x</body></html>"), fetcherFor(pages), llm)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	json.Unmarshal(res.Spec, &doc)
	paths, _ := doc["paths"].(map[string]any)
	for _, p := range []string{"/a", "/b", "/c"} {
		if _, has := paths[p]; !has {
			t.Fatalf("%s missing after the solo retry: %v", p, keysOf(paths))
		}
	}
	if res.ReferencePages != 3 || res.Endpoints != 3 || res.Retried != 1 || res.Recovered != 1 {
		t.Fatalf("counts: refs=%d endpoints=%d retried=%d recovered=%d", res.ReferencePages, res.Endpoints, res.Retried, res.Recovered)
	}
	if len(cap.requests) != 2 {
		t.Fatalf("one batch call plus one solo retry, got %d calls", len(cap.requests))
	}
	if !strings.Contains(cap.requests[0], "x-pikopod-source") {
		t.Fatal("the instruction must ask for the source page on every operation")
	}
	solo := strings.SplitN(cap.requests[1], "\n---\n", 2)[1]
	if strings.Contains(solo, a) || !strings.Contains(solo, c) {
		t.Fatalf("the solo retry must carry only the empty page:\n%s", solo[:min(len(solo), 400)])
	}
}

func TestPagesWithoutAMethodAreNotRetried(t *testing.T) {
	a, index := "https://docs.x.test/api-reference/a", "https://docs.x.test/api-reference"
	pages := map[string][]byte{
		"https://docs.x.test/llms.txt": llmsIndex(index, a),
		index:                          []byte(strings.Repeat("This section lists the operations of the API. ", 30)),
		a:                              refPage("/a"),
	}
	cap := &captured{}
	res, err := FromDocsURL("https://docs.x.test/intro", []byte("<html><body>x</body></html>"), fetcherFor(pages), fakeModel(t, cap, func(string) string { return specWith(opFor("/a", a)) }))
	if err != nil {
		t.Fatal(err)
	}
	if len(cap.requests) != 1 || res.Retried != 0 {
		t.Fatalf("an index page with no HTTP method is never retried: calls=%d retried=%d", len(cap.requests), res.Retried)
	}
}

func TestSoloRetriesAreCapped(t *testing.T) {
	var links []string
	pages := map[string][]byte{}
	for i := 0; i < maxSoloRetries+3; i++ {
		u := fmt.Sprintf("https://docs.x.test/api-reference/op-%d", i)
		links = append(links, u)
		pages[u] = refPage(fmt.Sprintf("/op-%d", i))
	}
	pages["https://docs.x.test/llms.txt"] = llmsIndex(links...)
	cap := &captured{}
	res, err := FromDocsURL("https://docs.x.test/intro", []byte("<html><body>x</body></html>"), fetcherFor(pages), fakeModel(t, cap, func(string) string { return emptySpec }))
	if err != nil {
		t.Fatal(err)
	}
	if res.Retried != maxSoloRetries {
		t.Fatalf("retries must stop at the cap: %d", res.Retried)
	}
	if len(res.Unretried) != 3 {
		t.Fatalf("pages past the cap must be reported: %v", res.Unretried)
	}
}
