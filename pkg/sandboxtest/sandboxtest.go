package sandboxtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/importer"
	"github.com/pikopod/pikopod/internal/ir"
	"github.com/pikopod/pikopod/internal/mode"
	"github.com/pikopod/pikopod/internal/sandbox"
	"github.com/pikopod/pikopod/internal/scenario/resolve"
)

type Option func(*options)

type options struct {
	seed     string
	seedData map[string][]map[string]any
	packDirs []string
	sink     string
}

func WithSeed(seed string) Option {
	return func(o *options) { o.seed = seed }
}

func WithSeedData(items map[string][]map[string]any) Option {
	return func(o *options) { o.seedData = items }
}

func WithPackDir(dir string) Option {
	return func(o *options) { o.packDirs = append(o.packDirs, dir) }
}

func WithWebhookSink(url string) Option {
	return func(o *options) { o.sink = url }
}

type Sandbox struct {
	t      testing.TB
	def    *ir.ApiDefinition
	engine *sandbox.Engine
	store  *sandbox.Store
	server *httptest.Server
	seed   string
	packs  []string
	mode   *mode.Spec
}

type Result struct {
	Status  string
	Summary string
	Steps   []Step
}

type Step struct {
	Name    string
	Status  string
	Summary string
}

func (r Result) Passed() bool { return r.Status == "PASSED" }

type Fault struct {
	Kind    string
	Method  string
	Path    string
	Status  int
	DelayMs int64
	Times   int
	Per     string
	Event   string
}

type Request struct {
	Method string
	Path   string
	Status int
}

func New(t testing.TB, spec []byte, opts ...Option) *Sandbox {
	t.Helper()
	o := options{seed: "sandboxtest"}
	for _, opt := range opts {
		opt(&o)
	}
	def, err := normalize(spec)
	if err != nil {
		t.Fatalf("sandboxtest: %v", err)
	}
	store, err := sandbox.OpenMemoryStore()
	if err != nil {
		t.Fatalf("sandboxtest: %v", err)
	}
	engine, err := sandbox.NewEngine(def, sandbox.Config{ID: "sbx_test", Seed: o.seed, Mode: "deterministic", WebhookURL: o.sink}, store)
	if err != nil {
		store.Close()
		t.Fatalf("sandboxtest: %v", err)
	}
	if len(o.seedData) > 0 {
		if _, err := engine.Seed(o.seedData); err != nil {
			engine.Close()
			store.Close()
			t.Fatalf("sandboxtest: %v", err)
		}
	}
	s := &Sandbox{t: t, def: def, engine: engine, store: store, seed: o.seed, packs: o.packDirs}
	s.server = httptest.NewServer(engine)
	t.Cleanup(s.Close)
	return s
}

func normalize(spec []byte) (*ir.ApiDefinition, error) {
	kind, err := importer.Detect(spec)
	if err != nil {
		return nil, err
	}
	switch kind {
	case importer.KindOpenAPI:
		return importer.NormalizeOpenAPI(spec)
	case importer.KindPostman:
		return importer.NormalizePostman(spec)
	case importer.KindGraphQLSDL:
		return importer.NormalizeGraphQLSDL(spec)
	case importer.KindGraphQLIntrospection:
		return importer.NormalizeGraphQLIntrospection(spec)
	}
	return nil, fmt.Errorf("the spec is not OpenAPI, Swagger, Postman or GraphQL; import documentation pages with the pikopod CLI first")
}

func (s *Sandbox) URL() string { return s.server.URL }

func (s *Sandbox) Credential() string { return s.engine.Credential() }

func (s *Sandbox) AuthHeader() (name, value string) {
	name, value, ok := s.engine.AuthHeader()
	if !ok {
		return "", ""
	}
	return name, value
}

type authTransport struct {
	name, value string
	next        http.RoundTripper
}

func (a authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if a.name != "" && r.Header.Get(a.name) == "" {
		r = r.Clone(r.Context())
		r.Header.Set(a.name, a.value)
	}
	return a.next.RoundTrip(r)
}

func (s *Sandbox) Client() *http.Client {
	name, value := s.AuthHeader()
	return &http.Client{Transport: authTransport{name: name, value: value, next: http.DefaultTransport}}
}

func (s *Sandbox) Mode(name string, binds ...string) {
	s.t.Helper()
	overrides := map[string]string{}
	for _, b := range binds {
		role, op, ok := strings.Cut(b, "=")
		if !ok {
			s.t.Fatalf("sandboxtest: bind %q is not role=operationId", b)
		}
		overrides[role] = op
	}
	parsed, info, err := resolve.ResolveDetailed(s.def, name, resolve.Options{PackDirs: s.packs, BindOverrides: overrides})
	if err != nil {
		s.t.Fatalf("sandboxtest: %v", err)
	}
	source := "archetype or pack " + name
	if note := info.Note(); note != "" {
		source += "; " + note
	}
	spec, err := mode.Compile(name, source, parsed)
	if err != nil {
		s.t.Fatalf("sandboxtest: %v", err)
	}
	if s.mode != nil {
		spec.Revision = s.mode.Revision + 1
	}
	if err := mode.Apply(s.engine, spec); err != nil {
		s.t.Fatalf("sandboxtest: %v", err)
	}
	s.mode = spec
}

func (s *Sandbox) ClearMode() {
	s.engine.ClearFaults("", "")
	s.mode = nil
}

func (s *Sandbox) Verify() Result {
	s.t.Helper()
	if s.mode == nil {
		s.t.Fatal("sandboxtest: Verify needs a Mode first")
	}
	res, err := mode.Verify(s.engine, s.mode, s.seed)
	if err != nil {
		s.t.Fatalf("sandboxtest: %v", err)
	}
	out := Result{Status: res.Status, Summary: res.Summary}
	for _, st := range res.Steps {
		out.Steps = append(out.Steps, Step{Name: st.Key, Status: st.Status, Summary: st.Summary})
	}
	return out
}

func (s *Sandbox) Chaos(f Fault) {
	s.t.Helper()
	if !sandbox.ValidFaultKind(f.Kind) {
		s.t.Fatalf("sandboxtest: fault kind %q is not one of %s", f.Kind, strings.Join(sandbox.FaultKinds(), ", "))
	}
	kind, status := sandbox.ResolveFaultKind(f.Kind, f.Status)
	s.engine.ArmFault(sandbox.FaultRule{Kind: kind, Status: status, Method: f.Method, Path: f.Path, DelayMs: f.DelayMs, Times: f.Times, Per: f.Per, Event: f.Event, Probability: 1})
}

func (s *Sandbox) ClearFaults() int { return s.engine.ClearFaults("", "") }

func (s *Sandbox) Emit(event string, data map[string]any) {
	s.t.Helper()
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			s.t.Fatalf("sandboxtest: %v", err)
		}
		raw = b
	}
	if err := s.engine.EmitWebhook(event, raw); err != nil {
		s.t.Fatalf("sandboxtest: %v", err)
	}
}

func (s *Sandbox) Requests() []Request {
	entries, _ := s.engine.JournalEntries(0)
	out := make([]Request, 0, len(entries))
	for _, e := range entries {
		out = append(out, Request{Method: e.Method, Path: e.Path, Status: e.Status})
	}
	return out
}

func (s *Sandbox) Seed(items map[string][]map[string]any) int {
	s.t.Helper()
	n, err := s.engine.Seed(items)
	if err != nil {
		s.t.Fatalf("sandboxtest: %v", err)
	}
	return n
}

func (s *Sandbox) Reset() {
	s.t.Helper()
	if err := s.engine.Reset(); err != nil {
		s.t.Fatalf("sandboxtest: %v", err)
	}
	s.mode = nil
}

func (s *Sandbox) Close() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
	if s.engine != nil {
		s.engine.Close()
		s.engine = nil
	}
	if s.store != nil {
		s.store.Close()
		s.store = nil
	}
}
