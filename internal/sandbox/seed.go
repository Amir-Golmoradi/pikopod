package sandbox

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/ir"
)

const maxSnapshots = 16

const seedDocs = "docs/config-reference.md#data_dir"

type seedState struct {
	mu        sync.Mutex
	items     map[string][]map[string]any
	count     int
	snapshots map[string][]ResourceRecord
	order     []string
	seq       int
}

func normalizeSeedType(key string) string {
	return "/" + strings.Trim(strings.TrimSpace(key), "/")
}

func (e *Engine) seedEndpoint(typ string) (create *ir.Endpoint, any_ *ir.Endpoint) {
	for i := range e.def.Endpoints {
		ep := &e.def.Endpoints[i]
		op := deriveOperation(ep, map[string]string{})
		if op.typ != typ {
			continue
		}
		if op.kind == opCreate && create == nil {
			create = ep
		}
		if any_ == nil {
			any_ = ep
		}
	}
	return create, any_
}

func (e *Engine) Seed(items map[string][]map[string]any) (int, error) {
	e.seeding.mu.Lock()
	defer e.seeding.mu.Unlock()
	if e.seeding.items == nil {
		e.seeding.items = map[string][]map[string]any{}
	}
	keys := make([]string, 0, len(items))
	for k := range items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	inserted := 0
	for _, key := range keys {
		typ := normalizeSeedType(key)
		create, anyOp := e.seedEndpoint(typ)
		if anyOp == nil {
			return inserted, errfmt.New("no operation stores "+key, "the spec declares no route whose resource type is "+typ, "name the seed keys after collection paths the spec declares, like charges for /charges", seedDocs)
		}
		for i, item := range items[key] {
			stored, err := e.seedOne(typ, create, item)
			if err != nil {
				return inserted, errfmt.Newf("seed item "+strconv.Itoa(i)+" of "+key+" refused", "fix the item so it would pass the create operation", seedDocs, "%v", err)
			}
			e.seeding.items[key] = append(e.seeding.items[key], stored)
			e.seeding.count++
			inserted++
		}
	}
	return inserted, nil
}

func (e *Engine) seedOne(typ string, create *ir.Endpoint, item map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	parsed, err := parseJSONValue(string(raw))
	if err != nil {
		return nil, err
	}
	obj, ok := parsed.(*JSONObject)
	if !ok {
		return nil, fmt.Errorf("a seed item must be a JSON object")
	}
	if create != nil {
		if invalid := validateBodyWith(requestSchema(create), obj, e.namedSchemas); len(invalid) > 0 {
			return nil, fmt.Errorf("%s", renderViolations(invalid))
		}
	}
	key := ""
	if v, has := obj.Get("id"); has {
		key = fmt.Sprint(v)
	}
	if key == "" {
		seq, err := e.store.AllocateSeq(e.id)
		if err != nil {
			return nil, err
		}
		key = typeSlug(typ) + "_" + strconv.FormatInt(seq, 10)
		obj.Set("id", key)
	}
	marshaled, err := marshalJSValue(obj)
	if err != nil {
		return nil, err
	}
	if _, err := e.store.Insert(e.id, typ, key, marshaled, nil, e.virtualClockMs); err != nil {
		return nil, fmt.Errorf("%s %s already exists", typ, key)
	}
	var out map[string]any
	if err := json.Unmarshal(marshaled, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (e *Engine) SetSeedItems(items map[string][]map[string]any) {
	e.seeding.mu.Lock()
	defer e.seeding.mu.Unlock()
	e.seeding.items = map[string][]map[string]any{}
	e.seeding.count = 0
	for k, v := range items {
		e.seeding.items[k] = append([]map[string]any(nil), v...)
		e.seeding.count += len(v)
	}
}

func (e *Engine) SeededCount() int {
	e.seeding.mu.Lock()
	defer e.seeding.mu.Unlock()
	return e.seeding.count
}

func (e *Engine) SeedItems() map[string][]map[string]any {
	e.seeding.mu.Lock()
	defer e.seeding.mu.Unlock()
	out := make(map[string][]map[string]any, len(e.seeding.items))
	for k, v := range e.seeding.items {
		out[k] = append([]map[string]any(nil), v...)
	}
	return out
}

func (e *Engine) Snapshot() string {
	recs, err := e.store.Serialize(e.id)
	if err != nil {
		return ""
	}
	e.seeding.mu.Lock()
	defer e.seeding.mu.Unlock()
	if e.seeding.snapshots == nil {
		e.seeding.snapshots = map[string][]ResourceRecord{}
	}
	e.seeding.seq++
	token := "snap_" + strconv.Itoa(e.seeding.seq)
	e.seeding.snapshots[token] = recs
	e.seeding.order = append(e.seeding.order, token)
	if len(e.seeding.order) > maxSnapshots {
		delete(e.seeding.snapshots, e.seeding.order[0])
		e.seeding.order = e.seeding.order[1:]
	}
	return token
}

func (e *Engine) Restore(token string) error {
	e.seeding.mu.Lock()
	recs, ok := e.seeding.snapshots[token]
	e.seeding.mu.Unlock()
	if !ok {
		return errfmt.New("unknown snapshot "+token, "the token names no snapshot this sandbox holds; the newest "+strconv.Itoa(maxSnapshots)+" are kept", "take a new one with POST /_pikopod/sandboxes/<name>/snapshot", seedDocs)
	}
	if err := e.store.Clear(e.id); err != nil {
		return err
	}
	_, err := e.store.Load(e.id, recs)
	return err
}

func (e *Engine) Reset() error {
	if err := e.store.Clear(e.id); err != nil {
		return err
	}
	e.seeding.mu.Lock()
	items := e.seeding.items
	e.seeding.items = map[string][]map[string]any{}
	e.seeding.count = 0
	e.seeding.mu.Unlock()
	if len(items) > 0 {
		if _, err := e.Seed(items); err != nil {
			return err
		}
	}
	e.ClearFaults("", "")
	e.ResetJournal()
	e.idemMu.Lock()
	e.idem = map[string]idemRecord{}
	e.idemMu.Unlock()
	return nil
}
