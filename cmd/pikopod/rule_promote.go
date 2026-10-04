package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"

	"github.com/pikopod/pikopod/internal/alert"
	"github.com/pikopod/pikopod/internal/bridge"
	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/ir"
	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/sandbox"
	"github.com/spf13/cobra"
)

func newRulePromoteCmd() *cobra.Command {
	c := &cobra.Command{Use: "promote <fingerprint>", Short: "Turn a divergence into a rule the sandbox keeps, after two questions", Args: cobra.ExactArgs(1),
		Long: `A divergence says the provider answered a recorded request differently from
the sandbox. Promoting it writes a rule: the recorded request's features become
the condition and the provider's real response becomes the answer, with
provenance promoted:<fingerprint>. Before writing, it shows the divergence,
the redacted request, the real response and the rule, and asks whether this is
the provider's behaviour rather than this integration's payload, and whether it
holds for every caller.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			yes, _ := cmd.Flags().GetBool("yes")
			return rulePromote(cfg, args[0], yes, cmd.InOrStdin(), cmd.OutOrStdout())
		}}
	c.Flags().Bool("yes", false, "skip the two questions")
	return c
}

func rulePromote(cfg *config.Config, fp string, yes bool, in io.Reader, out io.Writer) error {
	ev, err := bridge.FindEvent(cfg.DataDir, fp)
	if err != nil {
		return err
	}
	if !ev.Kind.IsDivergence() {
		return errfmt.New("not a divergence", string(ev.Kind)+" is not something the provider answered differently from the sandbox",
			"an incident is reproduced with `pikopod reproduce "+fp+"`; a shape change is pinned with `pikopod scenario from-drift "+fp+"`", rulesDocs)
	}
	rec, err := bridge.FindRecording(cfg.DataDir, ev)
	if err != nil {
		return err
	}
	entry, err := linkedSandbox(cfg, ev.Upstream)
	if err != nil {
		return err
	}
	_, def, err := loadSandboxDef(cfg, entry.Name)
	if err != nil {
		return err
	}
	rule, skipped := promotedRule(ev, rec)
	if len(rule.When.Body) == 0 {
		return errfmt.New("nothing in the request survived redaction to condition on",
			"every feature the divergence named was dropped before disk, so the rule would answer every "+ev.Method+" "+ev.Endpoint,
			"write the rule by hand with `pikopod rule add "+entry.Name+" <file>` and a condition you know", rulesDocs)
	}

	fmt.Fprintf(out, "divergence %s on %s %s (%s): %s\n", fp, ev.Method, ev.Endpoint, ev.Upstream, ev.Category)
	fmt.Fprintf(out, "  %s\n\n", ev.Detail)
	fmt.Fprintf(out, "recorded request (redacted):\n  %s %s\n", rec.Method, rec.Path)
	if rec.ReqBody != nil {
		fmt.Fprintf(out, "  %s\n", compactJSON(rec.ReqBody))
	}
	fmt.Fprintf(out, "\nprovider's response:\n  %d\n", rec.Status)
	if rec.RespBody != nil {
		fmt.Fprintf(out, "  %s\n", compactJSON(rec.RespBody))
	}
	for _, s := range skipped {
		fmt.Fprintf(out, "\nnot conditioned on %s: its value was dropped before disk, so the rule may be broader than the provider's behaviour\n", s)
	}
	pretty, _ := json.MarshalIndent(rule, "", "  ")
	fmt.Fprintf(out, "\nrule to write to %s:\n%s\n\n", sandbox.RulesPath(cfg.DataDir, entry.Name), pretty)

	if !yes {
		if !ask(in, out, "Is this the provider's behaviour, not this integration's payload? [y/N] ") || !ask(in, out, "Does it hold for every caller? [y/N] ") {
			fmt.Fprintln(out, "nothing written")
			return nil
		}
	}

	path := sandbox.RulesPath(cfg.DataDir, entry.Name)
	rs, err := sandbox.LoadRuleSet(path)
	if err != nil {
		return err
	}
	before := &sandbox.RuleSet{Version: rs.Version, Rules: append([]sandbox.Rule(nil), rs.Rules...)}
	rs.Rules = append(rs.Rules, rule)
	if err := sandbox.ValidateRules(def, rs); err != nil {
		return err
	}
	if err := sandbox.SaveRuleSet(path, rs); err != nil {
		return err
	}
	fmt.Fprintf(out, "added %s (set version %d)\n", rule.ID, rs.Version)
	if err := reportPush(cfg, entry.Name, rs, out); err != nil {
		return err
	}

	records, _ := readRecordings(cfg.DataDir, ev.Upstream)
	was, now := replayUpTo(cfg, entry, def, before, records, rec), replayUpTo(cfg, entry, def, rs, records, rec)
	fmt.Fprintf(out, "\nthe sandbox now answers the recorded request differently: before: %s  after: %s\n", was, now)
	return nil
}

func ask(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprint(out, question)
	var answer string
	fmt.Fscanln(in, &answer)
	fmt.Fprintln(out)
	return strings.HasPrefix(strings.ToLower(answer), "y")
}

func promotedRule(ev *alert.DriftEvent, rec *proxy.Record) (sandbox.Rule, []string) {
	rule := sandbox.Rule{
		ID:         "promoted-" + strings.TrimPrefix(ev.Fingerprint, "fp_"),
		Provenance: "promoted:" + ev.Fingerprint,
		When:       sandbox.RuleWhen{Method: ev.Method, Path: ev.Endpoint, Body: map[string]json.RawMessage{}},
		Respond:    sandbox.RuleRespond{Status: rec.Status},
	}
	if rec.RespKind == "json" && rec.RespBody != nil {
		if raw, err := json.Marshal(rec.RespBody); err == nil {
			rule.Respond.Body = raw
		}
	}
	body, _ := rec.ReqBody.(map[string]any)
	modes := map[string]string{}
	for _, r := range rec.Redacted {
		if r.Section == "req_body" {
			modes[strings.TrimPrefix(r.Pointer, "/")] = r.Mode
		}
	}
	var skipped []string
	features := strings.Split(ev.Field, ",")
	sort.Strings(features)
	for _, f := range features {
		f = strings.TrimSpace(f)
		name, ok := strings.CutPrefix(f, "body.")
		if !ok || name == "" || strings.Contains(name, ".") {
			continue
		}
		switch modes[name] {
		case "DROP":
			skipped = append(skipped, f)
			continue
		case "TOKENIZE", "SUBSTITUTE":
			rule.When.Body[name] = json.RawMessage(`"exists_in_store"`)
			continue
		}
		value, present := body[name]
		if !present {
			skipped = append(skipped, f)
			continue
		}
		raw, err := json.Marshal(map[string]any{"equals": value})
		if err != nil {
			continue
		}
		rule.When.Body[name] = raw
	}
	if len(rule.When.Body) == 0 {
		rule.When.Body = nil
	}
	return rule, skipped
}

func compactJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

func replayUpTo(cfg *config.Config, entry *sandboxEntry, def *ir.ApiDefinition, rs *sandbox.RuleSet, records []*proxy.Record, last *proxy.Record) string {
	st, err := sandbox.OpenMemoryStore()
	if err != nil {
		return "?"
	}
	defer st.Close()
	eng, err := sandbox.NewEngine(def, sandbox.Config{ID: entry.ID + "_promote", Seed: entry.Seed, Mode: entry.Mode, VirtualClockMs: entry.CreatedClockMs, Rules: rs, RecordingsMode: "off"}, st)
	if err != nil {
		return "?"
	}
	defer eng.Close()
	status := 0
	for _, r := range records {
		if r.TS.After(last.TS) {
			continue
		}
		status = replayRecord(eng, r)
	}
	if status == 0 {
		status = replayRecord(eng, last)
	}
	return strconv.Itoa(status)
}

func replayRecord(eng *sandbox.Engine, r *proxy.Record) int {
	var body []byte
	if r.ReqKind == "json" && r.ReqBody != nil {
		body, _ = json.Marshal(r.ReqBody)
	}
	req := httptest.NewRequest(strings.ToUpper(r.Method), r.Path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if name, value, ok := eng.AuthHeader(); ok {
		req.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	return w.Code
}
