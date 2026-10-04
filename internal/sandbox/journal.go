package sandbox

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/pikopod/pikopod/internal/sanitize"
)

const maxJournalEntries = 1024

const maxJournalBodyBytes = 16 * 1024

const (
	maxJournalHeaders     = 64
	maxJournalQueryValues = 64
)

type JournalEntry struct {
	Seq      int64  `json:"seq"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Template string `json:"template,omitempty"`
	Status   int    `json:"status"`

	Body             any               `json:"body,omitempty"`
	BodyTruncated    bool              `json:"bodyTruncated,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	HeadersTruncated bool              `json:"headersTruncated,omitempty"`

	Query          map[string][]string `json:"query,omitempty"`
	QueryTruncated bool                `json:"queryTruncated,omitempty"`

	AtMs   int64 `json:"atMs"`
	WallMs int64 `json:"wallMs"`

	scope string
}

type journal struct {
	mu      sync.Mutex
	entries []JournalEntry
	seq     int64
	evicted int64
}

func (e *Engine) journalHeaders(h map[string]string) (map[string]string, bool) {
	if len(h) == 0 {
		return nil, false
	}
	if len(h) > maxJournalHeaders {
		return nil, true
	}
	flat := make(map[string]any, len(h))
	for k, v := range h {
		flat[k] = v
	}
	res := sanitize.Sanitize(flat, e.journalTok, nil, true)
	obj, _ := res.Sanitized.(map[string]any)
	out := make(map[string]string, len(obj))
	for k, v := range obj {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out, false
}

func (e *Engine) journalQuery(q map[string][]string) (map[string][]string, bool) {
	if len(q) == 0 {
		return nil, false
	}
	total := 0
	for _, vs := range q {
		total += len(vs)
	}
	if total > maxJournalQueryValues {
		return nil, true
	}

	flat := make(map[string]any, len(q))
	for k, vs := range q {
		vals := make([]any, len(vs))
		for i, v := range vs {
			vals[i] = v
		}
		flat[k] = vals
	}
	res := sanitize.Sanitize(flat, e.journalTok, nil, false)
	obj, _ := res.Sanitized.(map[string]any)
	out := make(map[string][]string, len(obj))
	for k, v := range obj {
		vals, ok := v.([]any)
		if !ok {
			continue
		}
		kept := make([]string, 0, len(vals))
		for _, item := range vals {
			if s, ok := item.(string); ok {
				kept = append(kept, s)
			}
		}
		out[k] = kept
	}
	return out, false
}

func plainJSON(v any) any {
	raw, err := marshalJSValue(v)
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func (j *journal) record(entry JournalEntry) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	entry.Seq = j.seq
	j.entries = append(j.entries, entry)
	if len(j.entries) > maxJournalEntries {
		drop := len(j.entries) - maxJournalEntries
		j.entries = append(j.entries[:0:0], j.entries[drop:]...)
		j.evicted += int64(drop)
	}
}

func (entry *JournalEntry) matches(method, template string) bool {
	if method != "" && !strings.EqualFold(entry.Method, method) {
		return false
	}
	if template == "" {
		return true
	}
	return templateSegmentsMatch(template, entry.Template) || templateSegmentsMatch(template, entry.Path)
}

func templateSegmentsMatch(query, target string) bool {
	if target == "" {
		return false
	}
	q := strings.Split(strings.Trim(query, "/"), "/")
	tg := strings.Split(strings.Trim(target, "/"), "/")
	if len(q) != len(tg) {
		return false
	}
	for i := range q {
		queryTemplated := strings.Contains(q[i], "{")
		targetTemplated := strings.Contains(tg[i], "{")
		if queryTemplated || targetTemplated {
			continue
		}
		if q[i] != tg[i] {
			return false
		}
	}
	return true
}

func (entry *JournalEntry) inScope(scope string, only bool) bool {
	return !only || entry.scope == scope
}

func (j *journal) count(scope string, only bool, method, template string) (count int, evicted bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := range j.entries {
		if j.entries[i].inScope(scope, only) && j.entries[i].matches(method, template) {
			count++
		}
	}
	return count, j.evicted > 0
}

func (j *journal) last(scope string, only bool, method, template string) (entry *JournalEntry, found, evicted bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := len(j.entries) - 1; i >= 0; i-- {
		if j.entries[i].inScope(scope, only) && j.entries[i].matches(method, template) {
			cp := j.entries[i]
			return &cp, true, j.evicted > 0
		}
	}
	return nil, false, j.evicted > 0
}

func (j *journal) list(scope string, only bool, limit int) (entries []JournalEntry, evicted int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]JournalEntry, 0, len(j.entries))
	for i := range j.entries {
		if j.entries[i].inScope(scope, only) {
			out = append(out, j.entries[i])
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, j.evicted
}

func (j *journal) dropScope(scope string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	kept := j.entries[:0]
	for _, entry := range j.entries {
		if entry.scope != scope {
			kept = append(kept, entry)
		}
	}
	j.entries = kept
}

func (e *Engine) JournalCount(method, template string) (count int, evicted bool) {
	return e.journal.count("", false, method, template)
}

func (e *Engine) JournalLast(method, template string) (entry *JournalEntry, found, evicted bool) {
	return e.journal.last("", false, method, template)
}

func (e *Engine) JournalEntries(limit int) (entries []JournalEntry, evicted int64) {
	return e.journal.list("", false, limit)
}

func (e *Engine) ResetJournal() {
	e.journal.mu.Lock()
	e.journal.entries = nil
	e.journal.evicted = 0
	e.journal.mu.Unlock()
	e.resetScopes()
}

func (e *Engine) ResetJournalScope(scope string) {
	e.journal.dropScope(scope)
}

type SequenceFields struct {
	Method  string
	Headers map[string]string
	Query   map[string]string
}

func (entry *JournalEntry) MatchesSequence(f SequenceFields, path string) bool {
	if !entry.matches(f.Method, path) {
		return false
	}
	for k, want := range f.Headers {
		if entry.Headers[strings.ToLower(k)] != want {
			return false
		}
	}
	for k, want := range f.Query {
		vals := entry.Query[k]
		hit := false
		for _, v := range vals {
			if v == want {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}
