package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pikopod/pikopod/internal/bridge"
	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/ir"
	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/sandbox"
	"github.com/spf13/cobra"
)

const importDocs = "docs/config-reference.md#retention"

func newIncidentsImportCmd() *cobra.Command {
	return &cobra.Command{Use: "import <bundle.json>",
		Short: "Install an incident bundle from another host: the event, a rule that answers as the provider did, and the resource it touched",
		Long: `Reads a bundle written by pikopod agent incidents export on the host that recorded
the incident and makes the local sandbox behave as production did: the event and
its redacted recording are installed in the local log, a rule with provenance
imported:<fingerprint> answers the recorded route with the recorded status and
body, conditioned on the resource state the bundle carries, and that resource is
seeded into the sandbox's store. Importing the same bundle twice changes nothing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return incidentImport(cfg, args[0], cmd.OutOrStdout())
		}}
}

func incidentImport(cfg *config.Config, path string, out io.Writer) error {
	b, err := bridge.LoadBundle(path)
	if err != nil {
		return err
	}
	fp := b.Event.Fingerprint
	entry, err := linkedSandbox(cfg, b.Event.Upstream)
	if err != nil {
		return err
	}
	_, def, err := loadSandboxDef(cfg, entry.Name)
	if err != nil {
		return err
	}

	evs, err := loadEvents(cfg.DataDir)
	if err != nil {
		return err
	}
	known := false
	for _, ev := range evs {
		if ev.Fingerprint == fp {
			known = true
			break
		}
	}
	if known {
		fmt.Fprintf(out, "event %s already in the local log\n", fp)
	} else {
		if err := appendJSONLine(filepath.Join(cfg.DataDir, "events.ndjson"), b.Event); err != nil {
			return err
		}
		fmt.Fprintf(out, "event %s installed in the local log\n", fp)
	}
	if err := installRecording(cfg.DataDir, &b.Recording); err != nil {
		return err
	}

	ruleID := "imported-" + strings.TrimPrefix(fp, "fp_")
	rulesPath := sandbox.RulesPath(cfg.DataDir, entry.Name)
	rs, err := sandbox.LoadRuleSet(rulesPath)
	if err != nil {
		return err
	}
	hasRule := false
	for _, r := range rs.Rules {
		if r.ID == ruleID {
			hasRule = true
			break
		}
	}
	if hasRule {
		fmt.Fprintf(out, "rule %s already on %s\n", ruleID, entry.Name)
	} else {
		features := requestFeatures(&b.Recording)
		rule, _ := ruleFromRecording(ruleID, "imported:"+fp, b.Event.Method, declaredTemplate(def, b.Event.Method, b.Recording.Path, b.Event.Endpoint), &b.Recording, features)
		if b.State != nil && b.State.State != nil {
			cond, _ := json.Marshal(map[string]string{"state_is": *b.State.State})
			rule.When.State = map[string]json.RawMessage{b.State.Type: cond}
		}
		rs.Rules = append(rs.Rules, rule)
		if err := sandbox.ValidateRules(def, rs); err != nil {
			return err
		}
		if err := sandbox.SaveRuleSet(rulesPath, rs); err != nil {
			return err
		}
		fmt.Fprintf(out, "rule %s answers %s %s with %d (set version %d); drop it with `pikopod rule drop %s %s`\n", ruleID, rule.When.Method, rule.When.Path, b.Recording.Status, rs.Version, entry.Name, ruleID)
		if err := reportPush(cfg, entry.Name, rs, out); err != nil {
			return err
		}
	}

	if b.State != nil {
		st, err := sandbox.OpenStore(cfg.DataDir)
		if err != nil {
			return err
		}
		_, exists := st.GetOne(entry.ID, b.State.Type, b.State.Key)
		if exists == nil {
			fmt.Fprintf(out, "seeded %s %s already in the store\n", b.State.Type, b.State.Key)
		} else if _, err := st.Insert(entry.ID, b.State.Type, b.State.Key, b.State.Attributes, b.State.State, entry.CreatedClockMs); err != nil {
			st.Close()
			return errfmt.Newf("cannot seed the resource", "check permissions on "+cfg.DataDir, importDocs, "%v", err)
		} else {
			state := "no state"
			if b.State.State != nil {
				state = "state " + *b.State.State
			}
			fmt.Fprintf(out, "seeded /%s %s into %s (%s)\n", strings.Trim(b.State.Type, "/"), b.State.Key, entry.Name, state)
		}
		st.Close()
	} else {
		fmt.Fprintln(out, "no resource state in the bundle; nothing seeded")
	}
	fmt.Fprintf(out, "reproduce: pikopod reproduce %s\n", fp)
	return nil
}

func declaredTemplate(def *ir.ApiDefinition, method, path, fallback string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	want := strings.Split(strings.Trim(path, "/"), "/")
	for i := range def.Endpoints {
		ep := &def.Endpoints[i]
		if !strings.EqualFold(ep.Method.Value, method) {
			continue
		}
		have := strings.Split(strings.Trim(ep.PathTemplate.Value, "/"), "/")
		if len(have) != len(want) {
			continue
		}
		ok := true
		for j := range have {
			if !strings.HasPrefix(have[j], "{") && have[j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return ep.PathTemplate.Value
		}
	}
	return fallback
}

func requestFeatures(rec *proxy.Record) []string {
	body, ok := rec.ReqBody.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for k := range body {
		out = append(out, "body."+k)
	}
	return out
}

func appendJSONLine(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return errfmt.Newf("cannot write "+path, "check permissions on the data directory", importDocs, "%v", err)
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}

func installRecording(dataDir string, rec *proxy.Record) error {
	existing, _ := readRecordings(dataDir, rec.Upstream)
	for _, r := range existing {
		if r.TS.Equal(rec.TS) && r.Method == rec.Method && r.Path == rec.Path && r.Status == rec.Status {
			return nil
		}
	}
	return appendJSONLine(filepath.Join(dataDir, "recordings", rec.Upstream+".ndjson"), rec)
}
