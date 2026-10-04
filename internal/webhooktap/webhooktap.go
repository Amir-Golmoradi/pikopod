package webhooktap

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/pikopod/pikopod/internal/ir"
	"github.com/pikopod/pikopod/internal/proxy"
)

type Extractor struct {
	envelopes map[string]*ir.WebhookEnvelope
}

func New(envelopes map[string]*ir.WebhookEnvelope) *Extractor {
	if envelopes == nil {
		envelopes = map[string]*ir.WebhookEnvelope{}
	}
	return &Extractor{envelopes: envelopes}
}

var resourcePointers = []string{"/data/id", "/data/object/id", "/object/id", "/resource/id", "/payload/id"}

func (x *Extractor) Extract(ex *proxy.Exchange) *proxy.Delivery {
	d := &proxy.Delivery{}
	var body any
	if len(ex.ReqBody) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(ex.ReqBody)))
		dec.UseNumber()
		if dec.Decode(&body) != nil {
			body = nil
		}
	}
	payload := body
	env := x.envelopes[ex.Upstream]
	if env != nil {
		for name, tmpl := range env.Headers {
			x.assign(d, tmpl, ex.ReqHeader.Get(name))
		}
		if obj, ok := body.(map[string]any); ok {
			for field, tmpl := range env.Wrap {
				raw, present := obj[field]
				if !present {
					continue
				}
				switch strings.TrimSpace(tmpl) {
				case "{{" + ir.EnvelopeRefBody + "}}":
					payload = raw
				case "{{" + ir.EnvelopeRefBodyString + "}}":
					if s, isString := raw.(string); isString {
						var inner any
						if json.Unmarshal([]byte(s), &inner) == nil {
							payload = inner
						}
					}
				default:
					x.assign(d, tmpl, scalar(raw))
				}
			}
		}
	} else {
		d.Event = ex.ReqHeader.Get("x-pikopod-webhook-event")
		d.ID = ex.ReqHeader.Get("x-pikopod-webhook-id")
		d.EventTS = parseTimestamp(ex.ReqHeader.Get("x-pikopod-webhook-timestamp"), false)
	}
	if obj, ok := body.(map[string]any); ok {
		if d.Event == "" {
			d.Event = firstString(obj, "type", "event", "event_type", "topic")
		}
		if d.ID == "" {
			d.ID = firstString(obj, "id", "event_id", "delivery_id")
		}
		if d.EventTS == 0 {
			for _, k := range []string{"created", "created_at", "timestamp", "occurred_at", "time"} {
				if v, present := obj[k]; present {
					if ts := parseTimestamp(scalar(v), false); ts > 0 {
						d.EventTS = ts
						break
					}
				}
			}
		}
	}
	for _, p := range resourcePointers {
		if v, ok := lookup(payload, p); ok && v != "" {
			d.Resource = v
			break
		}
	}
	if d.Resource == "" {
		if obj, ok := payload.(map[string]any); ok {
			if v := firstString(obj, "id"); v != "" && v != d.ID {
				d.Resource = v
			}
		}
	}
	return d
}

func (x *Extractor) assign(d *proxy.Delivery, tmpl, value string) {
	if value == "" {
		return
	}
	switch strings.TrimSpace(tmpl) {
	case "{{" + ir.EnvelopeRefEvent + "}}":
		d.Event = value
	case "{{" + ir.EnvelopeRefID + "}}", "{{" + ir.EnvelopeRefUUID + "}}":
		d.ID = value
	case "{{" + ir.EnvelopeRefTimestamp + "}}":
		d.EventTS = parseTimestamp(value, false)
	case "{{" + ir.EnvelopeRefTimestampM + "}}":
		d.EventTS = parseTimestamp(value, true)
	case "{{" + ir.EnvelopeRefNow + "}}":
		d.EventTS = parseTimestamp(value, false)
	}
}

func parseTimestamp(s string, millis bool) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if millis || n > 1_000_000_000_000 {
			return n
		}
		return n * 1000
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f * 1000)
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli()
	}
	return 0
}

func scalar(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case json.Number:
		return n.String()
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(n)
	}
	return ""
}

func firstString(obj map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			if s := scalar(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func lookup(body any, pointer string) (string, bool) {
	node := body
	for _, seg := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		obj, ok := node.(map[string]any)
		if !ok {
			return "", false
		}
		node, ok = obj[seg]
		if !ok {
			return "", false
		}
	}
	s := scalar(node)
	return s, s != ""
}
