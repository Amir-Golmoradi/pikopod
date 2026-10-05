package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pikopod/pikopod/internal/sandbox"
)

var (
	forkIdleTimeout = 10 * time.Minute
	forkReapEvery   = time.Minute
)

type forkInfo struct {
	parent   string
	id       string
	eng      *sandbox.Engine
	created  time.Time
	lastSeen time.Time
}

type forkSet struct {
	mu    sync.Mutex
	forks map[string]*forkInfo
	once  sync.Once
	stop  chan struct{}
	seq   int
}

func (s *sandboxServer) registryName(name string) string {
	s.forks.mu.Lock()
	defer s.forks.mu.Unlock()
	if f, ok := s.forks.forks[name]; ok {
		return f.parent
	}
	return name
}

func (s *sandboxServer) touchFork(name string) {
	s.forks.mu.Lock()
	if f, ok := s.forks.forks[name]; ok {
		f.lastSeen = time.Now()
	}
	s.forks.mu.Unlock()
}

func (s *sandboxServer) newFork(parent string) (map[string]any, error) {
	h, err := s.handlerFor(parent)
	if err != nil || h == nil {
		return nil, err
	}
	parentEng, ok := h.(*sandbox.Engine)
	if !ok {
		return nil, nil
	}
	s.mu.Lock()
	entry, ok := s.entries[parent]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	s.forks.mu.Lock()
	s.forks.seq++
	seq := s.forks.seq
	s.forks.mu.Unlock()
	sum := sha256.Sum256([]byte(entry.Seed + "|fork|" + strconv.Itoa(seq)))
	token := hex.EncodeToString(sum[:4])
	name := parent + "--" + token
	eng, err := s.buildEngine(entry, entry.ID+"--"+token, "/"+name)
	if err != nil {
		return nil, err
	}
	if items := parentEng.SeedItems(); len(items) > 0 {
		if _, err := eng.Seed(items); err != nil {
			eng.Close()
			return nil, err
		}
	}
	now := time.Now()
	s.forks.mu.Lock()
	if s.forks.forks == nil {
		s.forks.forks = map[string]*forkInfo{}
	}
	s.forks.forks[name] = &forkInfo{parent: parent, id: entry.ID + "--" + token, eng: eng, created: now, lastSeen: now}
	s.forks.mu.Unlock()
	s.mu.Lock()
	s.handlers[name] = eng
	s.mu.Unlock()
	s.forks.once.Do(func() {
		stop := make(chan struct{})
		s.forks.mu.Lock()
		s.forks.stop = stop
		s.forks.mu.Unlock()
		go s.reapForks(stop)
	})
	return map[string]any{
		"name": name, "url": "/" + name + "/", "credential": eng.Credential(),
		"seeded": eng.SeededCount(), "expires_in_seconds": int(forkIdleTimeout.Seconds()),
	}, nil
}

func (s *sandboxServer) dropFork(name string) bool {
	s.forks.mu.Lock()
	f, ok := s.forks.forks[name]
	if ok {
		delete(s.forks.forks, name)
	}
	s.forks.mu.Unlock()
	if !ok {
		return false
	}
	s.mu.Lock()
	delete(s.handlers, name)
	delete(s.modes, name)
	s.mu.Unlock()
	f.eng.Close()
	s.store.Clear(f.id)
	return true
}

func (s *sandboxServer) listForks(parent string) []map[string]any {
	s.forks.mu.Lock()
	defer s.forks.mu.Unlock()
	out := []map[string]any{}
	for name, f := range s.forks.forks {
		if f.parent != parent {
			continue
		}
		out = append(out, map[string]any{"name": name, "url": "/" + name + "/", "created": f.created.UTC().Format(time.RFC3339), "idle_seconds": int(time.Since(f.lastSeen).Seconds())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out
}

func (s *sandboxServer) reapForks(stop <-chan struct{}) {
	tick := time.NewTicker(forkReapEvery)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			var idle []string
			s.forks.mu.Lock()
			for name, f := range s.forks.forks {
				if time.Since(f.lastSeen) > forkIdleTimeout {
					idle = append(idle, name)
				}
			}
			s.forks.mu.Unlock()
			for _, name := range idle {
				s.dropFork(name)
			}
		}
	}
}

func (s *sandboxServer) stopReaper() {
	s.forks.mu.Lock()
	stop := s.forks.stop
	s.forks.stop = nil
	s.forks.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (s *sandboxServer) serveDayZero(w http.ResponseWriter, r *http.Request, name, action string, engine *sandbox.Engine) {
	switch action {
	case "seed":
		if r.Method != http.MethodPost {
			writeSandboxJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		var items map[string][]map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&items); err != nil {
			writeSandboxJSONError(w, http.StatusBadRequest, "body must map collection names to lists of objects")
			return
		}
		n, err := engine.Seed(items)
		if err != nil {
			writeSandboxJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !strings.Contains(name, "--") {
			writeSeedItems(s.cfg.DataDir, name, mergeSeedItems(readSeedItems(s.cfg.DataDir, name), items))
		}
		json.NewEncoder(w).Encode(map[string]any{"seeded": n, "total": engine.SeededCount()})
	case "snapshot":
		if r.Method != http.MethodPost {
			writeSandboxJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"token": engine.Snapshot()})
	case "restore":
		if r.Method != http.MethodPost {
			writeSandboxJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		var body struct {
			Token string `json:"token"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		if err := engine.Restore(body.Token); err != nil {
			writeSandboxJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"restored": body.Token})
	case "reset":
		if r.Method != http.MethodPost {
			writeSandboxJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		if err := engine.Reset(); err != nil {
			writeSandboxJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.mu.Lock()
		delete(s.modes, name)
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"reset": true, "seeded": engine.SeededCount()})
	case "fork":
		switch r.Method {
		case http.MethodPost:
			info, err := s.newFork(name)
			if err != nil {
				writeSandboxJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if info == nil {
				writeSandboxJSONError(w, http.StatusNotFound, "unknown sandbox "+name)
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(info)
		case http.MethodDelete:
			if !s.dropFork(name) {
				writeSandboxJSONError(w, http.StatusNotFound, name+" is not a fork")
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"deleted": name})
		default:
			writeSandboxJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	case "forks":
		json.NewEncoder(w).Encode(map[string]any{"forks": s.listForks(name)})
	}
}
