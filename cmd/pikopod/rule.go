package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/sandbox"
	"github.com/spf13/cobra"
)

const rulesDocs = "docs/config-reference.md#rules"

func newRuleCmd() *cobra.Command {
	c := &cobra.Command{Use: "rule", Short: "Answer a declared operation by rule when the request or the stored state looks a certain way",
		Long: `Rules live beside the imported spec in <data_dir>/apis/<sandbox>.rules.json and
load with it. They are consulted after auth and faults and before the operation;
the first match wins. A rule that names a route, an event or an example the
spec does not declare is refused by name.

When ` + "`pikopod up`" + ` is serving the sandbox, add and drop apply to it at once.`}

	list := &cobra.Command{Use: "list <sandbox>", Short: "Show every rule: id, when, respond, provenance, version", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			entry, def, err := loadSandboxDef(cfg, args[0])
			if err != nil {
				return err
			}
			rs, err := sandbox.LoadRuleSet(sandbox.RulesPath(cfg.DataDir, entry.Name))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(rs.Rules) == 0 {
				fmt.Fprintf(out, "no rules on %s\n", entry.Name)
				return nil
			}
			rows, err := sandbox.DescribeRules(def, rs)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tWHEN\tRESPOND\tPROVENANCE\tVERSION")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", r.ID, r.When, r.Respond, r.Provenance, r.Version)
			}
			w.Flush()
			fmt.Fprintf(out, "%d rule(s), set version %d\n", len(rows), rs.Version)
			return nil
		}}

	add := &cobra.Command{Use: "add <sandbox> <file>", Short: "Add the rule(s) in a YAML or JSON file, validated against the spec", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			entry, def, err := loadSandboxDef(cfg, args[0])
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(args[1])
			if err != nil {
				return errfmt.Newf("cannot read the rule file", "check the path", rulesDocs, "%v", err)
			}
			incoming, err := sandbox.ParseRuleFile(raw)
			if err != nil {
				return err
			}
			path := sandbox.RulesPath(cfg.DataDir, entry.Name)
			rs, err := sandbox.LoadRuleSet(path)
			if err != nil {
				return err
			}
			rs.Rules = append(rs.Rules, incoming...)
			if err := sandbox.ValidateRules(def, rs); err != nil {
				return err
			}
			if err := sandbox.SaveRuleSet(path, rs); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, r := range incoming {
				fmt.Fprintf(out, "added %s (set version %d)\n", r.ID, rs.Version)
			}
			return reportPush(cfg, entry.Name, rs, out)
		}}

	drop := &cobra.Command{Use: "drop <sandbox> <id>", Short: "Remove a rule by id", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			entry, _, err := loadSandboxDef(cfg, args[0])
			if err != nil {
				return err
			}
			path := sandbox.RulesPath(cfg.DataDir, entry.Name)
			rs, err := sandbox.LoadRuleSet(path)
			if err != nil {
				return err
			}
			kept := rs.Rules[:0]
			found := false
			for _, r := range rs.Rules {
				if r.ID == args[1] {
					found = true
					continue
				}
				kept = append(kept, r)
			}
			if !found {
				return errfmt.New("no rule named "+args[1], "the set on "+entry.Name+" has no rule with that id", "see `pikopod rule list "+entry.Name+"`", rulesDocs)
			}
			rs.Rules = kept
			if err := sandbox.SaveRuleSet(path, rs); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "dropped %s (set version %d)\n", args[1], rs.Version)
			return reportPush(cfg, entry.Name, rs, out)
		}}

	check := &cobra.Command{Use: "check <sandbox>", Short: "Report rules that shadow a declared response unconditionally or can never fire (exit 1 when any)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			findings, err := ruleCheck(cfg, args[0], cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if findings > 0 {
				os.Exit(1)
			}
			return nil
		}}

	c.AddCommand(list, add, drop, check)
	return c
}

func ruleCheck(cfg *config.Config, name string, out io.Writer) (int, error) {
	entry, def, err := loadSandboxDef(cfg, name)
	if err != nil {
		return 0, err
	}
	rs, err := sandbox.LoadRuleSet(sandbox.RulesPath(cfg.DataDir, entry.Name))
	if err != nil {
		return 0, err
	}
	findings := sandbox.CheckRules(def, rs)
	if len(findings) == 0 {
		fmt.Fprintf(out, "no problems in %d rule(s) on %s\n", len(rs.Rules), entry.Name)
		return 0, nil
	}
	kinds := map[string]string{"shadows": "shadows", "never-fires": "never fires", "invalid": "invalid"}
	for _, f := range findings {
		fmt.Fprintf(out, "%-12s %-20s %s\n", kinds[f.Kind], f.RuleID, f.Message)
	}
	fmt.Fprintf(out, "\n%d problem(s) in %d rule(s) on %s — exit 1\n", len(findings), len(rs.Rules), entry.Name)
	return len(findings), nil
}

func reportPush(cfg *config.Config, name string, rs *sandbox.RuleSet, out io.Writer) error {
	applied, err := pushRules(cfg, name, rs)
	if err != nil {
		return err
	}
	if applied {
		fmt.Fprintf(out, "applied to the running sandbox %s\n", name)
	} else {
		fmt.Fprintf(out, "saved; applies when `pikopod up` serves %s\n", name)
	}
	return nil
}

func pushRules(cfg *config.Config, name string, rs *sandbox.RuleSet) (bool, error) {
	raw, err := json.Marshal(rs)
	if err != nil {
		return false, err
	}
	base := fmt.Sprintf("%s://%s:%d/_pikopod/v1/sandboxes/%s/rules", cfg.Scheme(), cfg.Listen, cfg.SandboxPort, name)
	req, err := http.NewRequest(http.MethodPut, base, bytes.NewReader(raw))
	if err != nil {
		return false, err
	}
	req.Header.Set("content-type", "application/json")
	if token := cfg.Token(); token != "" {
		req.Header.Set("X-Pikopod-Token", token)
	}
	resp, err := cfg.LocalClient(0).Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return false, errfmt.New("the running sandbox refused the rules", strconv.Itoa(resp.StatusCode)+": "+string(bytes.TrimSpace(body)), "fix the rule and retry", rulesDocs)
	}
	return true, nil
}
