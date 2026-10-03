package divergence

import (
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/proxy"
)

func record(method, path string, status int, req, resp map[string]any, redacted ...proxy.SectionRedaction) *proxy.Record {
	rec := &proxy.Record{Upstream: "pay", Method: method, Path: path, Status: status, RespKind: "json", RespBody: resp, Redacted: redacted}
	if req != nil {
		rec.ReqKind, rec.ReqBody = "json", req
	}
	return rec
}

func dropped(section, pointer string) proxy.SectionRedaction {
	return proxy.SectionRedaction{Section: section, Pointer: pointer, Mode: "DROP"}
}

func TestRefusalOfWhatTheSpecAcceptsIsAnUndocumentedRule(t *testing.T) {
	rec := record("POST", "/accounts", 422, map[string]any{"tier": "gold"}, map[string]any{}, dropped("req_body", "/display_name"), dropped("resp_body", "/error"))
	v := Compare(rec, Side{Status: 201, Body: map[string]any{"id": "accounts_1"}}, map[int]bool{201: true})
	if !v.Diverged || v.Unverifiable || v.Category != UndocumentedRule {
		t.Fatalf("201 by the spec, 422 by the provider: %+v", v)
	}
	if strings.Join(v.Features, ",") != "body.display_name,body.tier" {
		t.Fatalf("the request features are field names only, including dropped ones: %v", v.Features)
	}
	for _, want := range []string{"201", "422", "body.display_name"} {
		if !strings.Contains(v.Detail, want) {
			t.Fatalf("detail must name both statuses and the features: %q", v.Detail)
		}
	}
	if strings.Contains(v.Detail, "gold") {
		t.Fatalf("a request value leaked into the detail: %q", v.Detail)
	}
}

func TestMatchingStatusesWithoutADifferingCodeDoNotDiverge(t *testing.T) {
	rec := record("POST", "/accounts", 201, map[string]any{"tier": "gold"}, map[string]any{"id": "acc_tok", "tier": "gold"})
	if v := Compare(rec, Side{Status: 201, Body: map[string]any{"id": "accounts_1", "tier": "gold"}}, map[int]bool{201: true}); v.Diverged || v.Unverifiable {
		t.Fatalf("same status, no error fields: %+v", v)
	}
	codes := record("POST", "/accounts", 402, nil, map[string]any{"code": "card_declined"})
	if v := Compare(codes, Side{Status: 402, Body: map[string]any{"code": "card_declined"}}, map[int]bool{402: true}); v.Diverged {
		t.Fatalf("same status and the same error code: %+v", v)
	}
	if v := Compare(codes, Side{Status: 402, Body: map[string]any{"code": "insufficient_funds"}}, map[int]bool{402: true}); !v.Diverged || v.Category != UndocumentedRule {
		t.Fatalf("same status, a different error code: %+v", v)
	}
}

func TestRedactedEvidenceIsUnverifiableNeverAMatch(t *testing.T) {
	rec := record("POST", "/accounts", 400, nil, map[string]any{}, dropped("resp_body", "/error"))
	v := Compare(rec, Side{Status: 400, Body: map[string]any{"message": "display_name is required"}}, map[int]bool{201: true})
	if !v.Unverifiable || v.Diverged {
		t.Fatalf("the reason the comparison needs was dropped: %+v", v)
	}
}

func TestCategoriesFollowTheStatusPair(t *testing.T) {
	failing := record("POST", "/accounts", 503, nil, map[string]any{})
	if v := Compare(failing, Side{Status: 201}, map[int]bool{201: true}); !v.Diverged || v.Category != ProviderFailure {
		t.Fatalf("5xx from the provider: %+v", v)
	}
	accepted := record("POST", "/accounts", 201, map[string]any{"tier": "gold"}, map[string]any{"id": "acc_tok"})
	if v := Compare(accepted, Side{Status: 400, Body: map[string]any{"message": "tier is not allowed"}}, map[int]bool{201: true}); !v.Diverged || v.Category != SpecDrift {
		t.Fatalf("the provider accepted what the spec refuses: %+v", v)
	}
	okStatus := record("POST", "/accounts", 200, nil, map[string]any{"id": "acc_tok"})
	if v := Compare(okStatus, Side{Status: 201, Body: map[string]any{"id": "accounts_1"}}, map[int]bool{200: true, 201: true}); v.Diverged {
		t.Fatalf("both success codes are declared, the sandbox merely picked one: %+v", v)
	}
	if v := Compare(okStatus, Side{Status: 201, Body: map[string]any{"id": "accounts_1"}}, map[int]bool{201: true}); !v.Diverged || v.Category != SpecDrift {
		t.Fatalf("an undeclared success code is spec drift: %+v", v)
	}
	notFound := record("GET", "/accounts/acc_tok", 200, nil, map[string]any{"id": "acc_tok"})
	if v := Compare(notFound, Side{Status: 404, Body: map[string]any{"message": "Not Found"}}, map[int]bool{200: true}); !v.Skipped || v.Diverged {
		t.Fatalf("a resource the fork never stored is not comparable: %+v", v)
	}
}

func TestFingerprintIsStableAndCarriesNoValues(t *testing.T) {
	a := Fingerprint("pay", "POST", "/accounts", UndocumentedRule, 201, 422, []string{"body.display_name"})
	b := Fingerprint("pay", "POST", "/accounts", UndocumentedRule, 201, 422, []string{"body.display_name"})
	c := Fingerprint("pay", "POST", "/accounts", UndocumentedRule, 201, 422, []string{"body.tier"})
	if a != b || a == c || !strings.HasPrefix(a, "fp_") || len(a) != 15 {
		t.Fatalf("fingerprints: %s %s %s", a, b, c)
	}
}
