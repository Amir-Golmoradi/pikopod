package agent

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/divergence"
	"github.com/pikopod/pikopod/internal/drift"
	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/sandbox"
)

type ForkFactory func(upstream string) (*sandbox.Engine, func(), error)

const forkTTL = time.Minute

type fork struct {
	eng   *sandbox.Engine
	done  func()
	built time.Time
}

type forkCache struct {
	mu      sync.Mutex
	factory ForkFactory
	forks   map[string]*fork
}

func (a *Agent) SetSandboxFork(f ForkFactory) {
	a.forks.mu.Lock()
	defer a.forks.mu.Unlock()
	a.forks.factory = f
	if a.forks.forks == nil {
		a.forks.forks = map[string]*fork{}
	}
}

func (a *Agent) forkFor(upstream string) *sandbox.Engine {
	a.forks.mu.Lock()
	defer a.forks.mu.Unlock()
	if a.forks.factory == nil {
		return nil
	}
	if a.forks.forks == nil {
		a.forks.forks = map[string]*fork{}
	}
	f, ok := a.forks.forks[upstream]
	if ok && time.Since(f.built) < forkTTL {
		return f.eng
	}
	if ok && f.done != nil {
		f.done()
	}
	eng, done, err := a.forks.factory(upstream)
	if err != nil {
		a.forks.forks[upstream] = &fork{built: time.Now()}
		return nil
	}
	a.forks.forks[upstream] = &fork{eng: eng, done: done, built: time.Now()}
	return eng
}

func (a *Agent) closeForks() {
	a.forks.mu.Lock()
	defer a.forks.mu.Unlock()
	for name, f := range a.forks.forks {
		if f.done != nil {
			f.done()
		}
		delete(a.forks.forks, name)
	}
}

func (a *Agent) checkDivergence(rec *proxy.Record, template string) bool {
	if rec.RespKind != "json" {
		return false
	}
	eng := a.forkFor(rec.Upstream)
	if eng == nil {
		a.DivergenceSkipped.Add(1)
		return false
	}
	var body []byte
	if rec.ReqKind == "json" && rec.ReqBody != nil {
		body, _ = json.Marshal(rec.ReqBody)
	}
	req := httptest.NewRequest(strings.ToUpper(rec.Method), rec.Path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if name, value, ok := eng.AuthHeader(); ok {
		req.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	a.DivergenceChecked.Add(1)

	var forkBody any
	if w.Body.Len() > 0 {
		dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
		dec.UseNumber()
		if dec.Decode(&forkBody) != nil {
			forkBody = nil
		}
	}
	if w.Code >= 400 && droppedRequestFields(rec) {
		a.DivergenceUnverifiable.Add(1)
		return false
	}
	declared, _ := eng.DeclaredStatuses(rec.Method, pathOnly(rec.Path))
	v := divergence.Compare(rec, divergence.Side{Status: w.Code, Body: forkBody}, declared)
	switch {
	case v.Skipped:
		a.DivergenceSkipped.Add(1)
		return false
	case v.Unverifiable:
		a.DivergenceUnverifiable.Add(1)
		return false
	case !v.Diverged:
		return false
	}
	fp := divergence.Fingerprint(rec.Upstream, rec.Method, template, v.Category, w.Code, rec.Status, v.Features)
	a.EventsEmitted.Add(1)
	a.Alerter.ReportObserved(rec.Upstream, fp, alert.DriftEvent{
		Method: rec.Method, Endpoint: template, StatusClass: statusClass(rec.Status),
		Kind: drift.BehaviourDivergence, Category: string(v.Category),
		Field: strings.Join(v.Features, ","), Before: strconv.Itoa(w.Code), After: strconv.Itoa(rec.Status),
		Level: divergence.Level(v.Category), Detail: v.Detail,
	})
	return true
}

func droppedRequestFields(rec *proxy.Record) bool {
	for _, r := range rec.Redacted {
		if r.Section == "req_body" && r.Mode == "DROP" {
			return true
		}
	}
	return false
}

func pathOnly(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

func statusClass(status int) string {
	return strconv.Itoa(status/100) + "xx"
}
