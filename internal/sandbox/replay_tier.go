package sandbox

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/replay"
)

const ReplayTierHeader = "x-pikopod-replay-tier"

const ReplayMissedOnHeader = "x-pikopod-replay-missed-on"

const ReplayClosestHeader = "x-pikopod-replay-closest"

const ReplaySequenceHeader = "x-pikopod-replay-sequence"

const SourceHeader = "x-pikopod-source"

const sourceRecorded = "recorded"

func (e *Engine) serveRecording(req *ingressRequest, innerPath string) *RawResponse {
	rec, headers := e.matchRecording(req, innerPath, nil)
	if rec == nil {
		return nil
	}
	return e.recordedResponse(rec, headers)
}

func (e *Engine) matchRecording(req *ingressRequest, innerPath string, allow func(*proxy.Record, replay.MatchTier) bool) (*proxy.Record, map[string]string) {
	if e.recordings == nil {
		return nil, nil
	}

	reqBody := req.bodyValue
	if reqBody != nil {
		reqBody = plainJSON(reqBody)
	}
	rec, diag := e.recordings.MatchValueWhere(req.method, innerPath, reqBody, allow)
	if rec == nil {
		if diag.Closest != "" {
			e.tracef("recordings", "no recordings for %s %s; closest recorded endpoint: %s (%d recordings)",
				req.method, innerPath, diag.Closest, diag.ClosestN)
		} else {
			e.tracef("recordings", "no recordings for %s %s (nothing recorded nearby either)", req.method, innerPath)
		}
		return nil, nil
	}
	headers := map[string]string{ReplayTierHeader: string(diag.Tier), SourceHeader: sourceRecorded}
	if diag.SeqLen > 1 {

		seq := fmt.Sprintf("%d/%d", diag.SeqPos, diag.SeqLen)
		if diag.Held {
			seq += " (holding last)"
		}
		headers[ReplaySequenceHeader] = seq
		e.tracef("recordings", "exact-tier sequence step %s", seq)
	}
	if len(diag.MissedOn) > 0 {
		gap := strings.Join(diag.MissedOn, ",")
		switch diag.Tier {
		case replay.TierShape:
			headers[ReplayMissedOnHeader] = "exact missed on values: " + gap
			e.tracef("recordings", "shape-tier serve — exact missed on values: %s", gap)
		case replay.TierSequence:
			headers[ReplayMissedOnHeader] = "shape missed on fields: " + gap
			e.tracef("recordings", "sequence-tier serve — shape missed on fields: %s", gap)
		}
	}
	if e.effective != nil {
		headers[ContractVersionHeader] = strconv.Itoa(e.effective.Version)
	}
	return rec, headers
}

func (e *Engine) recordedResponse(rec *proxy.Record, headers map[string]string) *RawResponse {
	var body []byte
	if rec.RespBody != nil {
		var err error
		if body, err = json.Marshal(rec.RespBody); err != nil {
			return nil
		}
		headers["content-type"] = jsonContentType
		headers["content-length"] = strconv.Itoa(len(body))
		e.noteRecorded(rec.RespBody)
	}
	return &RawResponse{Status: rec.Status, Headers: headers, Body: body}
}

func (e *Engine) noteRecorded(body any) {
	switch v := body.(type) {
	case map[string]any:
		for _, k := range sortedKeys(v) {
			e.tracef("synth", "%s ← %s", k, sourceRecorded)
		}
	case []any:
		e.tracef("synth", "[%d items] ← %s", len(v), sourceRecorded)
	default:
		e.tracef("synth", "body ← %s", sourceRecorded)
	}
}

func allowRecordingFirst(rec *proxy.Record, tier replay.MatchTier) bool {
	return rec.Status < 400 || tier == replay.TierExact
}

func (e *Engine) recordedFirst(op *operation, req *ingressRequest, innerPath string) (*RawResponse, *proxy.Record) {
	if !e.recordingsFirst || e.recordings == nil {
		return nil, nil
	}
	switch op.kind {
	case opRead, opReplace, opMerge, opDelete:
		if op.key != nil {
			if _, err := e.store.GetOne(e.id, op.typ, *op.key); err == nil {
				e.tracef("recordings", "stored %s/%s answers before any recording", op.typ, *op.key)
				return nil, nil
			}
		}
	case opList:
		if page, err := e.store.List(e.id, op.typ, 1, nil); err == nil && len(page.Items) > 0 {
			e.tracef("recordings", "stored %s resources answer the list before any recording", op.typ)
			return nil, nil
		}
	}
	rec, headers := e.matchRecording(req, innerPath, allowRecordingFirst)
	if rec == nil {
		return nil, nil
	}
	if op.kind == opCreate && rec.Status < 400 {
		e.tracef("recordings", "recorded %d for %s %s is the template the created resource is completed from", rec.Status, req.method, innerPath)
		return nil, rec
	}
	e.tracef("recordings", "served from the recording (%s tier) before the spec", headers[ReplayTierHeader])
	return e.recordedResponse(rec, headers), nil
}

func recordedTemplate(rec *proxy.Record, slot string) (inner *JSONObject, outer *JSONObject) {
	raw, err := json.Marshal(rec.RespBody)
	if err != nil {
		return nil, nil
	}
	obj, ok := parseJSONValueOK(raw)
	if !ok {
		return nil, nil
	}
	if slot == "" {
		return obj, nil
	}
	if nested, has := obj.Get(slot); has {
		if innerObj, isObj := nested.(*JSONObject); isObj {
			return innerObj, obj
		}
	}
	return obj, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
