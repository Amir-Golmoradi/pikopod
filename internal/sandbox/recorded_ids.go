package sandbox

import (
	"strconv"
	"strings"
	"sync"
)

const IDRewrittenHeader = "x-pikopod-id-rewritten"

var maxRecordedIDs = 4096

type recordedID struct {
	typ string
	key string
	seq int64
}

type recordedIDs struct {
	mu      sync.Mutex
	byToken map[string]recordedID
	seq     int64
}

func (e *Engine) loadRecordedIDs() {
	rows, err := e.store.ListRecordedIDs(e.id)
	e.recordedIDs.mu.Lock()
	defer e.recordedIDs.mu.Unlock()
	e.recordedIDs.byToken = map[string]recordedID{}
	e.recordedIDs.seq = 0
	if err != nil {
		return
	}
	for _, r := range rows {
		e.recordedIDs.byToken[r.Token] = recordedID{typ: r.Type, key: r.ResourceKey, seq: r.Seq}
		if r.Seq > e.recordedIDs.seq {
			e.recordedIDs.seq = r.Seq
		}
	}
}

func (e *Engine) forgetRecordedIDs() {
	e.recordedIDs.mu.Lock()
	e.recordedIDs.byToken = map[string]recordedID{}
	e.recordedIDs.seq = 0
	e.recordedIDs.mu.Unlock()
}

func (e *Engine) rememberRecordedID(token, typ, key string) {
	if token == "" || key == "" || token == key {
		return
	}
	e.recordedIDs.mu.Lock()
	defer e.recordedIDs.mu.Unlock()
	if e.recordedIDs.byToken == nil {
		e.recordedIDs.byToken = map[string]recordedID{}
	}
	if _, known := e.recordedIDs.byToken[token]; !known && len(e.recordedIDs.byToken) >= maxRecordedIDs {
		victim, oldest := "", int64(0)
		for tok, r := range e.recordedIDs.byToken {
			if victim == "" || r.seq < oldest {
				victim, oldest = tok, r.seq
			}
		}
		delete(e.recordedIDs.byToken, victim)
		e.store.RemoveRecordedID(e.id, victim)
	}
	e.recordedIDs.seq++
	r := recordedID{typ: typ, key: key, seq: e.recordedIDs.seq}
	e.recordedIDs.byToken[token] = r
	e.store.PutRecordedID(e.id, token, typ, key, r.seq)
	e.tracef("recordings", "recorded id %s now resolves to stored %s/%s", token, typ, key)
}

func (e *Engine) resolveRecordedID(token string) (recordedID, bool) {
	e.recordedIDs.mu.Lock()
	defer e.recordedIDs.mu.Unlock()
	r, ok := e.recordedIDs.byToken[token]
	return r, ok
}

func (e *Engine) rewriteRecordedIDs(req *ingressRequest, innerPath string) (string, int) {
	e.recordedIDs.mu.Lock()
	empty := len(e.recordedIDs.byToken) == 0
	e.recordedIDs.mu.Unlock()
	if empty {
		return innerPath, 0
	}
	n := 0
	segments := strings.Split(innerPath, "/")
	for i, seg := range segments {
		if r, ok := e.resolveRecordedID(seg); ok {
			segments[i] = r.key
			n++
			e.tracef("recordings", "recorded id %s in the path resolves to stored %s/%s", seg, r.typ, r.key)
		}
	}
	if n > 0 {
		innerPath = strings.Join(segments, "/")
	}
	if req.bodyValue != nil {
		req.bodyValue = e.rewriteRecordedIDsIn(req.bodyValue, "", &n)
	}
	return innerPath, n
}

func (e *Engine) rewriteRecordedIDsIn(v any, path string, n *int) any {
	switch t := v.(type) {
	case *JSONObject:
		for _, k := range t.Keys() {
			child, _ := t.Get(k)
			t.Set(k, e.rewriteRecordedIDsIn(child, path+"/"+k, n))
		}
		return t
	case []any:
		for i := range t {
			t[i] = e.rewriteRecordedIDsIn(t[i], path+"/"+strconv.Itoa(i), n)
		}
		return t
	case string:
		if r, ok := e.resolveRecordedID(t); ok {
			*n++
			e.tracef("recordings", "recorded id %s at body%s resolves to stored %s/%s", t, path, r.typ, r.key)
			return r.key
		}
	}
	return v
}
