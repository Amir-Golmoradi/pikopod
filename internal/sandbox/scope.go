package sandbox

import (
	"strings"
	"sync"
)

const (
	ScopeHeader  = "x-pikopod-scope"
	MaxScopes    = 256
	maxScopeLen  = 128
	scopeKeyMark = "\x1f"
)

type scopeRegistry struct {
	mu      sync.Mutex
	seen    map[string]int64
	tick    int64
	evicted int64
}

func scopedKey(scope, key string) string {
	if scope == "" {
		return key
	}
	return scope + scopeKeyMark + key
}

func (e *Engine) touchScope(scope string) {
	if scope == "" {
		return
	}
	e.scopes.mu.Lock()
	if e.scopes.seen == nil {
		e.scopes.seen = map[string]int64{}
	}
	e.scopes.tick++
	_, known := e.scopes.seen[scope]
	victim := ""
	if !known && len(e.scopes.seen) >= MaxScopes {
		oldest := int64(0)
		for name, at := range e.scopes.seen {
			if victim == "" || at < oldest {
				victim, oldest = name, at
			}
		}
		delete(e.scopes.seen, victim)
		e.scopes.evicted++
	}
	e.scopes.seen[scope] = e.scopes.tick
	e.scopes.mu.Unlock()
	if victim != "" {
		e.forgetScope(victim)
	}
}

func (e *Engine) forgetScope(scope string) {
	e.journal.dropScope(scope)
	prefix := scope + scopeKeyMark
	e.faultMu.Lock()
	for i := range e.faults {
		dropPrefixed(e.faults[i].consumed, prefix)
	}
	e.faultMu.Unlock()
	e.rulesMu.Lock()
	for i := range e.rules {
		dropPrefixed(e.rules[i].consumed, prefix)
	}
	e.rulesMu.Unlock()
	if e.recordings != nil {
		e.recordings.ForgetScope(scope)
	}
}

func dropPrefixed(m map[string]int, prefix string) {
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			delete(m, k)
		}
	}
}

func (e *Engine) resetScopes() {
	e.scopes.mu.Lock()
	e.scopes.seen = nil
	e.scopes.evicted = 0
	e.scopes.mu.Unlock()
}

func (e *Engine) Scopes() (live int, evicted int64) {
	e.scopes.mu.Lock()
	defer e.scopes.mu.Unlock()
	return len(e.scopes.seen), e.scopes.evicted
}

type ScopedEngine struct {
	*Engine
	scope string
}

func (e *Engine) Scoped(scope string) *ScopedEngine {
	return &ScopedEngine{Engine: e, scope: scope}
}

func (s *ScopedEngine) Scope() string { return s.scope }

func (s *ScopedEngine) JournalCount(method, template string) (count int, evicted bool) {
	return s.Engine.journal.count(s.scope, true, method, template)
}

func (s *ScopedEngine) JournalLast(method, template string) (entry *JournalEntry, found, evicted bool) {
	return s.Engine.journal.last(s.scope, true, method, template)
}

func (s *ScopedEngine) JournalEntries(limit int) (entries []JournalEntry, evicted int64) {
	return s.Engine.journal.list(s.scope, true, limit)
}
