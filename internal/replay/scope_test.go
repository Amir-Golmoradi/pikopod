package replay

import (
	"testing"

	"github.com/pikopod/pikopod/internal/proxy"
)

func TestTheRecordingsCursorIsPartitionedByScope(t *testing.T) {
	dir := t.TempDir()
	writeRecordings(t, dir, "pay", []proxy.Record{
		{Method: "POST", Path: "/charge", Status: 200, ReqBody: body(`{"amount":100}`), RespBody: body(`{"id":"first"}`), RespKind: "json", ReqKind: "json"},
		{Method: "POST", Path: "/charge", Status: 200, ReqBody: body(`{"amount":250}`), RespBody: body(`{"id":"second"}`), RespKind: "json", ReqKind: "json"},
	})
	set, err := Load(dir, "pay", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := func(scope string) string {
		rec, _ := set.MatchValueScoped(scope, "POST", "/charge", body(`{"amount":999}`), nil)
		if rec == nil {
			return ""
		}
		return rec.RespBody.(map[string]any)["id"].(string)
	}
	if got := id("a"); got != "first" {
		t.Fatalf("scope a starts at the first recording: %q", got)
	}
	if got := id("b"); got != "first" {
		t.Fatalf("scope b has its own cursor: %q", got)
	}
	if got := id(""); got != "first" {
		t.Fatalf("the unscoped partition has its own cursor: %q", got)
	}
	if got := id("a"); got != "second" {
		t.Fatalf("scope a advances on its own: %q", got)
	}
	set.ForgetScope("a")
	if got := id("a"); got != "first" {
		t.Fatalf("a forgotten scope starts over: %q", got)
	}
	if got := id("b"); got != "second" {
		t.Fatalf("forgetting a leaves b alone: %q", got)
	}
}
