package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/pikopod/pikopod/internal/drift"
	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/replay"
	"github.com/pikopod/pikopod/internal/specwatch"
	"github.com/spf13/cobra"
)

func runReplay(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	ci, _ := cmd.Flags().GetBool("ci")
	out := cmd.OutOrStdout()

	if !ci {
		return errfmt.New("replay needs --ci", "--ci is the only mode: it diffs recordings offline against frozen baselines", "run `pikopod agent replay --ci [upstreams...]`", "docs/exit-codes.md")
	}

	failOnFlag, _ := cmd.Flags().GetString("fail-on")
	failOn, err := drift.ParseRisk(failOnFlag)
	if err != nil {
		return err
	}
	upstreams := args
	if len(upstreams) == 0 {
		upstreams = cfg.UpstreamNames()
	}
	totalFindings, totalRecords := 0, 0
	type handoffFinding struct {
		Upstream string `json:"upstream"`
		replay.GateFinding
	}
	handoffFindings, handoffAccepted := []handoffFinding{}, []handoffFinding{}
	for _, name := range upstreams {
		res, err := replay.GateWith(cfg.DataDir, name, cfg.Upstreams[name].VolatileFields, gateOptions(cfg.DataDir, name, failOn))
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %d recordings gated (%d pre-warmup skipped) — %d finding(s) at or above %s, %d accepted\n", name, res.Records, res.Skipped, len(res.Findings), failOn, len(res.Accepted))
		for _, f := range res.Findings {
			fmt.Fprintln(out, driftLine(f))
			handoffFindings = append(handoffFindings, handoffFinding{Upstream: name, GateFinding: f})
		}
		writeAccepted(out, res.Accepted, "below --fail-on "+string(failOn), "accepted (below --fail-on "+string(failOn)+"):")
		writeAccepted(out, res.Accepted, replay.AcceptedFingerprint, "accepted (fingerprint accepted with `pikopod agent accept`):")
		for _, f := range res.Accepted {
			handoffAccepted = append(handoffAccepted, handoffFinding{Upstream: name, GateFinding: f})
		}
		totalFindings += len(res.Findings)
		totalRecords += res.Records
	}
	if handoff, _ := cmd.Flags().GetString("handoff"); handoff != "" {
		raw, hErr := json.MarshalIndent(map[string]any{
			"source": "replay-ci", "records": totalRecords, "fail_on": string(failOn), "findings": handoffFindings, "accepted": handoffAccepted,
		}, "", "  ")
		if hErr != nil {
			return hErr
		}
		if hErr := os.WriteFile(handoff, append(raw, '\n'), 0o600); hErr != nil {
			return hErr
		}
	}
	if totalFindings > 0 {
		fmt.Fprintf(out, "\ndrift found — failing the gate (exit 1)\n")
		os.Exit(1)
	}
	fmt.Fprintln(out, "clean — no drift against frozen baselines")
	return nil
}

func gateOptions(dataDir, upstream string, failOn drift.Risk) replay.GateOptions {
	doc := specwatch.LoadDocumented(dataDir, upstream)
	return replay.GateOptions{FailOn: failOn, Annotate: func(fs []drift.Finding) { specwatch.AnnotateDocumented(fs, doc) }}
}

func driftLine(f replay.GateFinding) string {
	line := fmt.Sprintf("  DRIFT %-6s %-20s %s %s  %s %s", f.Risk, f.Kind, f.Method, f.Template, f.Field, f.Detail)
	if f.Documented {
		line += "  (documented)"
	}
	return line + "  " + f.Fingerprint
}

func writeAccepted(out io.Writer, accepted []replay.GateFinding, because, header string) {
	printed := false
	for _, f := range accepted {
		if f.Because != because {
			continue
		}
		if !printed {
			fmt.Fprintln(out, header)
			printed = true
		}
		fmt.Fprintln(out, driftLine(f))
	}
}
