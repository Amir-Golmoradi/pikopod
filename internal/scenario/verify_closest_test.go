package scenario

import (
	"strings"
	"testing"
)

func TestVerifySequenceNamesTheClosestRequestOnAPathMiss(t *testing.T) {
	def := parseDef(t, `{
	  "steps": [
	    {"key": "a1", "type": "REQUEST", "config": {"method": "POST", "path": "/widgets/w_8c1f2a", "body": {"name": "CANARYVALUE"}}},
	    {"key": "seq", "type": "VERIFY_SEQUENCE",
	     "config": {"requests": [{"method": "POST", "path": "/widgets"}]}}
	  ]
	}`)
	res, err := Run(runnerEngine(t, "vs-closest-1"), def, nil, "vs-closest-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != RunFailed {
		t.Fatalf("a POST to the wrong path never matches: %s", res.Status)
	}
	for _, want := range []string{"never matched", "closest: POST /widgets/w_{id}", "(request 1 of 1)", "differed on: path /widgets/w_{id} vs /widgets"} {
		if !strings.Contains(res.Summary, want) {
			t.Fatalf("missing %q in %q", want, res.Summary)
		}
	}
	if strings.Contains(res.Summary, "CANARYVALUE") {
		t.Fatalf("values never appear: %q", res.Summary)
	}
	last := res.Steps[len(res.Steps)-1]
	if closest, _ := last.Detail["closest"].(string); !strings.HasPrefix(closest, "closest: POST") {
		t.Fatalf("the closest text is in the step detail: %+v", last.Detail)
	}
}

func TestVerifySequenceNamesTheMissingHeaderWithoutItsValue(t *testing.T) {
	def := parseDef(t, `{
	  "steps": [
	    {"key": "a1", "type": "REQUEST", "config": {"method": "POST", "path": "/widgets", "headers": {"X-Trace": "CANARYHEADER"}, "body": {"name": "a"}}},
	    {"key": "seq", "type": "VERIFY_SEQUENCE",
	     "config": {"requests": [{"method": "POST", "path": "/widgets", "headers": {"Idempotency-Key": "CANARYKEY"}}]}}
	  ]
	}`)
	res, err := Run(runnerEngine(t, "vs-closest-2"), def, nil, "vs-closest-2")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != RunFailed || !strings.Contains(res.Summary, "differed on: header idempotency-key absent") {
		t.Fatalf("the missing header is named: %s %q", res.Status, res.Summary)
	}
	for _, leak := range []string{"CANARYKEY", "CANARYHEADER"} {
		if strings.Contains(res.Summary, leak) {
			t.Fatalf("values never appear: %q", res.Summary)
		}
	}
}

func TestVerifySequenceSaysWhenTheClientSimplyStopped(t *testing.T) {
	def := parseDef(t, `{
	  "steps": [
	    {"key": "a1", "type": "REQUEST", "config": {"method": "POST", "path": "/widgets"}},
	    {"key": "a2", "type": "REQUEST", "config": {"method": "POST", "path": "/widgets"}},
	    {"key": "seq", "type": "VERIFY_SEQUENCE",
	     "config": {"requests": [
	       {"method": "POST", "path": "/widgets"},
	       {"method": "POST", "path": "/widgets"},
	       {"method": "POST", "path": "/widgets"}
	     ]}}
	  ]
	}`)
	res, err := Run(runnerEngine(t, "vs-closest-3"), def, nil, "vs-closest-3")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != RunFailed || !strings.Contains(res.Summary, "already matched matcher 1; no later request matched") {
		t.Fatalf("when every candidate was consumed, say the client stopped: %s %q", res.Status, res.Summary)
	}
}
