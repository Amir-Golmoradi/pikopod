package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/sandbox"
	"github.com/pikopod/pikopod/internal/store"
	"gopkg.in/yaml.v3"
)

const seedDocs = "docs/config-reference.md#data_dir"

func seedPath(dataDir, name string) string {
	return filepath.Join(dataDir, "apis", name+".seed.json")
}

func loadSeedFile(path string) (map[string][]map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errfmt.Newf("cannot read the seed file", "check the path passed to --seed-data", seedDocs, "%v", err)
	}
	var generic map[string]any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return nil, errfmt.Newf("the seed file is not YAML or JSON", "a seed file maps a collection name to a list of objects, like widgets: [{id: w_1, name: gear}]", seedDocs, "%v", err)
	}
	out := map[string][]map[string]any{}
	for key, v := range generic {
		list, ok := v.([]any)
		if !ok {
			return nil, errfmt.New("seed key "+key+" is not a list", "every top-level key maps a collection to a list of objects", "write "+key+": [{...}, {...}]", seedDocs)
		}
		for i, item := range list {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, errfmt.New(fmt.Sprintf("seed item %d of %s is not an object", i, key), "every item becomes one stored resource", "write each item as a mapping with its fields", seedDocs)
			}
			out[key] = append(out[key], obj)
		}
	}
	return out, nil
}

func readSeedItems(dataDir, name string) map[string][]map[string]any {
	raw, err := os.ReadFile(seedPath(dataDir, name))
	if err != nil {
		return nil
	}
	var items map[string][]map[string]any
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return items
}

func writeSeedItems(dataDir, name string, items map[string][]map[string]any) error {
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(seedPath(dataDir, name), raw)
}

func countSeedItems(items map[string][]map[string]any) int {
	n := 0
	for _, list := range items {
		n += len(list)
	}
	return n
}

func mergeSeedItems(base, extra map[string][]map[string]any) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	for k, v := range base {
		out[k] = append(out[k], v...)
	}
	for k, v := range extra {
		out[k] = append(out[k], v...)
	}
	return out
}

func seedOffline(cfg *config.Config, entry *sandboxEntry, items map[string][]map[string]any) (int, error) {
	_, def, err := loadSandboxDef(cfg, entry.Name)
	if err != nil {
		return 0, err
	}
	st, err := sandbox.OpenStore(cfg.DataDir)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	eng, err := sandbox.NewEngine(def, sandbox.Config{ID: entry.ID, Seed: entry.Seed, Mode: entry.Mode, VirtualClockMs: entry.CreatedClockMs, RecordingsMode: "off"}, st)
	if err != nil {
		return 0, err
	}
	defer eng.Close()
	n, err := eng.Seed(items)
	if err != nil {
		return n, err
	}
	if err := writeSeedItems(cfg.DataDir, entry.Name, mergeSeedItems(readSeedItems(cfg.DataDir, entry.Name), eng.SeedItems())); err != nil {
		return n, err
	}
	return n, nil
}

func controlPlanePost(cfg *config.Config, name, action string, body []byte) (bool, int, map[string]any, error) {
	url := fmt.Sprintf("%s://%s:%d/_pikopod/v1/sandboxes/%s/%s", cfg.Scheme(), cfg.Listen, cfg.SandboxPort, name, action)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, 0, nil, err
	}
	req.Header.Set("content-type", "application/json")
	if token := cfg.Token(); token != "" {
		req.Header.Set("X-Pikopod-Token", token)
	}
	resp, err := cfg.LocalClient(0).Do(req)
	if err != nil {
		return false, 0, nil, nil
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	json.Unmarshal(raw, &out)
	return true, resp.StatusCode, out, nil
}

func sandboxSeed(cfg *config.Config, name, file string, out io.Writer) error {
	entries, err := loadRegistry(cfg.DataDir)
	if err != nil {
		return err
	}
	entry := findEntry(entries, name)
	if entry == nil {
		return errfmt.New("unknown sandbox", fmt.Sprintf("%q is not registered", name), "see `pikopod sandbox list`; add it with `pikopod import`", "")
	}
	items, err := loadSeedFile(file)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(items)
	reached, status, resp, err := controlPlanePost(cfg, name, "seed", raw)
	if err != nil {
		return err
	}
	if reached {
		if status >= 400 {
			msg, _ := resp["message"].(string)
			return errfmt.New("the running sandbox refused the seed", msg, "fix the seed file and retry", seedDocs)
		}
		fmt.Fprintf(out, "seeded %v resource(s) into the running sandbox %s (%v stored in total)\n", resp["seeded"], name, resp["total"])
		return nil
	}
	n, err := seedOffline(cfg, entry, items)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "seeded %d resource(s) into %s; they are served by the next `pikopod up`\n", n, name)
	return nil
}

var upSpecNameRE = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

type upSpecOptions struct {
	Spec     string
	Name     string
	Seed     string
	SeedData string
	Port     int
	Started  func(url string)
}

func upSpecName(spec string) string {
	base := filepath.Base(strings.TrimSuffix(strings.TrimSuffix(spec, "/"), "?"))
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimSuffix(base, ".spec")
	base = strings.TrimSuffix(base, ".openapi")
	name := strings.Trim(upSpecNameRE.ReplaceAllString(base, "-"), "-")
	if name == "" || name == "openapi" || name == "swagger" || name == "spec" {
		return "sandbox"
	}
	return name
}

func upSpec(ctx context.Context, opts upSpecOptions, out io.Writer) error {
	dir, err := os.MkdirTemp("", "pikopod-up-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cfg, err := config.Ephemeral(dir)
	if err != nil {
		return err
	}
	if opts.Port > 0 {
		cfg.SandboxPort = opts.Port
	}
	name := opts.Name
	if name == "" {
		name = upSpecName(opts.Spec)
	}
	if err := sandboxAddOpts(cfg, name, addOptions{SpecSource: opts.Spec, Seed: opts.Seed, Recordings: "off", SeedData: opts.SeedData}, out); err != nil {
		return err
	}
	sbx, err := newSandboxServer(cfg)
	if err != nil {
		return err
	}
	defer sbx.Close()
	listenAddr := net.JoinHostPort(cfg.Listen, fmt.Sprint(opts.Port))
	if opts.Port == 0 && opts.Started == nil {
		listenAddr = net.JoinHostPort(cfg.Listen, fmt.Sprint(cfg.SandboxPort))
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return errfmt.Newf("cannot listen on "+listenAddr, "stop whatever holds the port, or pass --port", "docs/config-reference.md#listen", "%v", err)
	}
	url := fmt.Sprintf("%s://%s", cfg.Scheme(), ln.Addr().String())
	fmt.Fprintf(out, "pikopod sandbox on %s  (sandboxes: %s) — temporary data dir, removed on exit\n", url, name)
	fmt.Fprintf(out, "no agent: --spec serves the sandbox only; add an upstream to pikopod.yaml to proxy and record real traffic\n")
	srv := &http.Server{Handler: sbx, ReadHeaderTimeout: 20 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	if opts.Started != nil {
		opts.Started(url)
	}
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		srv.Close()
	}
	return nil
}
