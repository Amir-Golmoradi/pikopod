package divergence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pikopod/pikopod/internal/proxy"
)

type Category string

const (
	SpecDrift        Category = "spec_drift"
	UndocumentedRule Category = "undocumented_rule"
	ProviderFailure  Category = "provider_failure"
)

type Side struct {
	Status int
	Body   any
}

type Verdict struct {
	Diverged     bool
	Unverifiable bool
	Skipped      bool
	Category     Category
	Features     []string
	Detail       string
	Reason       string
}

var codePointers = []string{"/code", "/error/code", "/error_code", "/type", "/error/type"}

var reasonPointers = []string{"/message", "/error/message", "/error", "/reason", "/error/reason", "/detail", "/error_description", "/errors/0/message"}

const maxFeatures = 8

func Compare(rec *proxy.Record, fork Side, declared map[int]bool) Verdict {
	v := Verdict{Features: Features(rec)}
	real := rec.Status
	switch fork.Status {
	case 401, 403, 404, 405:
		v.Skipped, v.Reason = true, fmt.Sprintf("the fork answered %d, so the request is not comparable", fork.Status)
		return v
	}
	if real >= 500 || real == 429 {
		if fork.Status != real {
			return v.diverge(ProviderFailure, fork.Status, real)
		}
		return v
	}
	realClass, forkClass := real/100, fork.Status/100
	switch {
	case realClass == 4 && forkClass == 2:
		return v.diverge(UndocumentedRule, fork.Status, real)
	case realClass == 2 && forkClass == 4:
		return v.diverge(SpecDrift, fork.Status, real)
	case realClass == 2 && forkClass == 2:
		if real == fork.Status || declared[real] {
			return v
		}
		return v.diverge(SpecDrift, fork.Status, real)
	case realClass == 4 && forkClass == 4:
		if real != fork.Status {
			if declared[real] {
				return v.diverge(UndocumentedRule, fork.Status, real)
			}
			return v.diverge(SpecDrift, fork.Status, real)
		}
		return v.compareErrorFields(rec, fork)
	}
	return v
}

func (v Verdict) compareErrorFields(rec *proxy.Record, fork Side) Verdict {
	realCode, codeRedacted := field(rec.RespBody, codePointers, rec)
	forkCode, _ := field(fork.Body, codePointers, nil)
	if codeRedacted {
		v.Unverifiable, v.Reason = true, "the error code the comparison needs was redacted"
		return v
	}
	if realCode != "" && forkCode != "" {
		if realCode != forkCode {
			return v.diverge(UndocumentedRule, fork.Status, rec.Status)
		}
		return v
	}
	realReason, reasonRedacted := field(rec.RespBody, reasonPointers, rec)
	forkReason, _ := field(fork.Body, reasonPointers, nil)
	if reasonRedacted {
		v.Unverifiable, v.Reason = true, "the reason the comparison needs was redacted"
		return v
	}
	if realReason != "" && forkReason != "" && realReason != forkReason {
		return v.diverge(UndocumentedRule, fork.Status, rec.Status)
	}
	return v
}

func (v Verdict) diverge(category Category, forkStatus, realStatus int) Verdict {
	v.Diverged, v.Category = true, category
	v.Detail = fmt.Sprintf("sandbox answered %d, provider answered %d", forkStatus, realStatus)
	if len(v.Features) > 0 {
		v.Detail += "; request carried " + strings.Join(v.Features, ", ")
	}
	return v
}

func Features(rec *proxy.Record) []string {
	set := map[string]bool{}
	if body, ok := rec.ReqBody.(map[string]any); ok {
		for k := range body {
			set["body."+k] = true
		}
	}
	for _, r := range rec.Redacted {
		if r.Section != "req_body" {
			continue
		}
		p := strings.TrimPrefix(r.Pointer, "/")
		if i := strings.IndexByte(p, '/'); i >= 0 {
			p = p[:i]
		}
		if p != "" {
			set["body."+p] = true
		}
	}
	if i := strings.IndexByte(rec.Path, '?'); i >= 0 {
		for _, pair := range strings.Split(rec.Path[i+1:], "&") {
			k, _, _ := strings.Cut(pair, "=")
			if k != "" {
				set["query."+k] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > maxFeatures {
		out = out[:maxFeatures]
	}
	return out
}

func field(body any, pointers []string, rec *proxy.Record) (value string, redacted bool) {
	for _, p := range pointers {
		if rec != nil && redactedAt(rec, p) {
			return "", true
		}
		if v, ok := lookup(body, p); ok {
			return v, false
		}
	}
	return "", false
}

func redactedAt(rec *proxy.Record, pointer string) bool {
	for _, r := range rec.Redacted {
		if r.Section == "resp_body" && r.Pointer == pointer {
			return true
		}
	}
	return false
}

func lookup(body any, pointer string) (string, bool) {
	node := body
	for _, seg := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		switch v := node.(type) {
		case map[string]any:
			child, ok := v[seg]
			if !ok {
				return "", false
			}
			node = child
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return "", false
			}
			node = v[i]
		default:
			return "", false
		}
	}
	switch v := node.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case float64, bool, int, int64:
		return fmt.Sprint(v), true
	}
	return "", false
}

func Fingerprint(upstream, method, template string, category Category, forkStatus, realStatus int, features []string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{upstream, method, template, "behaviour_divergence", string(category),
		strconv.Itoa(forkStatus) + "->" + strconv.Itoa(realStatus), strings.Join(features, ",")}, "|")))
	return "fp_" + hex.EncodeToString(h[:])[:12]
}

func Level(category Category) string {
	if category == ProviderFailure {
		return "ERR"
	}
	return "WARN"
}
