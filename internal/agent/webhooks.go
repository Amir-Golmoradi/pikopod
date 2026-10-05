package agent

import (
	"time"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/drift"
	"github.com/pikopod/pikopod/internal/ir"
	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/webhooktap"
)

const (
	webhookDedupeWindow = 24 * time.Hour
	webhookMaxTracked   = 4096
)

type webhookState struct {
	seen   map[string]time.Time
	order  []string
	lastTS map[string]int64
	byRes  []string
}

func (a *Agent) SetEnvelopes(m map[string]*ir.WebhookEnvelope) {
	a.Recorder.SetDeliveryExtractor(webhooktap.New(m).Extract)
}

func (a *Agent) webhookState(upstream string) *webhookState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.webhooks == nil {
		a.webhooks = map[string]*webhookState{}
	}
	st, ok := a.webhooks[upstream]
	if !ok {
		st = &webhookState{seen: map[string]time.Time{}, lastTS: map[string]int64{}}
		a.webhooks[upstream] = st
	}
	return st
}

func (a *Agent) observeDelivery(rec *proxy.Record) bool {
	d := rec.Delivery
	if d == nil {
		return true
	}
	st := a.webhookState(rec.Upstream)
	now := time.Now()
	a.mu.Lock()
	duplicate := false
	if d.ID != "" {
		if first, seen := st.seen[d.ID]; seen && now.Sub(first) <= webhookDedupeWindow {
			duplicate = true
		} else {
			if !seen {
				st.order = append(st.order, d.ID)
				if len(st.order) > webhookMaxTracked {
					delete(st.seen, st.order[0])
					st.order = st.order[1:]
				}
			}
			st.seen[d.ID] = now
		}
	}
	outOfOrder := false
	if d.Resource != "" && d.EventTS > 0 {
		last, known := st.lastTS[d.Resource]
		switch {
		case known && d.EventTS < last:
			outOfOrder = true
		case !known:
			st.byRes = append(st.byRes, d.Resource)
			if len(st.byRes) > webhookMaxTracked {
				delete(st.lastTS, st.byRes[0])
				st.byRes = st.byRes[1:]
			}
			st.lastTS[d.Resource] = d.EventTS
		case d.EventTS > last:
			st.lastTS[d.Resource] = d.EventTS
		}
	}
	a.mu.Unlock()
	if duplicate {
		a.webhookIncident(rec, d, drift.WebhookDuplicate, "WARN", "the same delivery id arrived twice within 24h")
	}
	if outOfOrder {
		a.webhookIncident(rec, d, drift.WebhookOutOfOrder, "ERR", "an older event for a resource arrived after a newer one")
	}
	return true
}

func (a *Agent) webhookIncident(rec *proxy.Record, d *proxy.Delivery, kind drift.Kind, level, detail string) {
	template := "/hooks/" + rec.Upstream
	f := drift.Finding{Upstream: rec.Upstream, Method: "POST", Template: template, Kind: kind, Field: d.Event}
	a.EventsEmitted.Add(1)
	a.Alerter.ReportObserved(rec.Upstream, f.Fingerprint(), alert.DriftEvent{
		Method: "POST", Endpoint: template, StatusClass: "", Kind: kind, Field: d.Event, Level: level, Detail: detail,
	})
}
