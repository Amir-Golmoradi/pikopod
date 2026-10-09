package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/sanitize"
)

func TestAnExcludedPathIsForwardedNeverWrittenAndCounted(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/health") {
			w.Write([]byte(`{"status":"HEALTHCANARY"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer up.Close()
	s := testServer(t, up.URL, 8)
	front := httptest.NewServer(s)
	defer front.Close()
	resp, err := http.Get(front.URL + "/examplepay/health?deep=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "HEALTHCANARY") {
		t.Fatalf("an excluded path is forwarded untouched: %d %s", resp.StatusCode, body)
	}

	dir := t.TempDir()
	m := &Metrics{}
	rec := NewRecorder(dir, sanitize.NewTokenizer("k", "local", 1), m)
	up1 := config.Upstream{Record: config.RecordFilter{Exclude: []string{"^/health", "^/kyc/documents"}}}
	rec.SetFilters(map[string]func(string) bool{"prov": up1.RecordExcluded})
	var observed []*Record
	rec.SetObserver(func(r *Record) bool {
		observed = append(observed, r)
		return false
	})
	ch := make(chan *Exchange)
	done := make(chan struct{})
	go func() { rec.Run(ch); close(done) }()
	ex := func(method, path string, status int, body string) *Exchange {
		return &Exchange{Upstream: "prov", Method: method, Path: path, Status: status, ReqBody: []byte(body), RespBody: []byte(`{"status":"HEALTHCANARY"}`), Start: time.Now()}
	}
	ch <- ex("GET", "/health?deep=1", 200, "")
	ch <- ex("POST", "/kyc/documents", 201, `{"passport":"P1234567"}`)
	ch <- ex("GET", "/health", 503, "")
	ch <- ex("POST", "/charges", 201, `{"amount":100}`)
	close(ch)
	<-done

	raw, err := os.ReadFile(filepath.Join(dir, "recordings", "prov.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "health") || strings.Contains(string(raw), "kyc") || strings.Contains(string(raw), "P1234567") || strings.Contains(string(raw), "HEALTHCANARY") {
		t.Fatalf("an excluded exchange never reaches disk, even redacted:\n%s", raw)
	}
	if lines := strings.Count(strings.TrimSpace(string(raw)), "\n") + 1; lines != 1 {
		t.Fatalf("only the charge is written: %d lines\n%s", lines, raw)
	}
	if m.RecordingsExcluded.Load() != 3 {
		t.Fatalf("excluded exchanges are counted: %d", m.RecordingsExcluded.Load())
	}
	if len(observed) != 2 {
		t.Fatalf("the observer sees the charge and the failed health check, never the healthy excluded ones: %d", len(observed))
	}
	var failed *Record
	for _, r := range observed {
		if r.Status == 503 {
			failed = r
		}
	}
	if failed == nil || !failed.Excluded || failed.RespBody != nil || failed.ReqBody != nil {
		t.Fatalf("a 5xx on an excluded path is observed as a fact with no body: %+v", failed)
	}
}

func TestIncludeKeepsOnlyWhatItNames(t *testing.T) {
	up := config.Upstream{Record: config.RecordFilter{Include: []string{"^/charges"}, Exclude: []string{"^/charges/internal"}}}
	for path, excluded := range map[string]bool{"/charges": false, "/charges/ch_1": false, "/charges/internal/x": true, "/health": true, "/refunds": true} {
		if got := up.RecordExcluded(path); got != excluded {
			t.Errorf("%s: excluded=%v want %v", path, got, excluded)
		}
	}
}
