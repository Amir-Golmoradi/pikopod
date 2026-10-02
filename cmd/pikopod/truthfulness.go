package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"

	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/proxy"
	"github.com/pikopod/pikopod/internal/truth"
	"github.com/spf13/cobra"
)

func newTruthfulnessCmd() *cobra.Command {
	c := &cobra.Command{Use: "truthfulness <upstream>", Short: "How many of the responses the real provider sent the linked sandbox reproduces",
		Long: `Replays every recorded exchange for an upstream against a fresh copy of the
sandbox linked to it and scores the sandbox's answer against what the provider
sent: the status, then every scalar field of the body. A field the recording
redacted is scored on presence and type only and counted as shape-only.

It needs recordings. Before any production traffic exists, point your app at
the agent in front of the provider's own test sandbox, run your tests once,
then run this.`, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("format")
			if format != "text" && format != "json" {
				return errfmt.New("unknown --format", fmt.Sprintf("%q is not text or json", format), "pass --format text or --format json", "docs/config-reference.md#data_dir")
			}
			report, err := truthfulnessFor(cfg, args[0], 0)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			io.WriteString(out, report.Text())
			return nil
		}}
	c.Flags().String("format", "text", "output format: text | json")
	return c
}

func linkedSandbox(cfg *config.Config, upstream string) (*sandboxEntry, error) {
	entries, err := loadRegistry(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Upstream == upstream {
			return &entries[i], nil
		}
	}
	if e := findEntry(entries, upstream); e != nil {
		return e, nil
	}
	return nil, errfmt.New("no sandbox is linked to "+upstream,
		"the truthfulness number compares recordings of "+upstream+" against the sandbox built from its spec, and no sandbox names it",
		"import the provider's spec with `pikopod import "+upstream+" --spec <file-or-url>`",
		"docs/config-reference.md#data_dir")
}

func truthfulnessFor(cfg *config.Config, upstream string, limit int) (*truth.Report, error) {
	entry, err := linkedSandbox(cfg, upstream)
	if err != nil {
		return nil, err
	}
	_, def, err := loadSandboxDef(cfg, entry.Name)
	if err != nil {
		return nil, err
	}
	records, err := readRecordings(cfg.DataDir, upstream)
	if err != nil {
		return nil, err
	}
	var jsonRecords []*proxy.Record
	for _, r := range records {
		if r.RespKind == "json" && r.RespBody != nil {
			jsonRecords = append(jsonRecords, r)
		}
	}
	if len(jsonRecords) == 0 {
		agentURL := fmt.Sprintf("%s://%s", cfg.Scheme(), net.JoinHostPort(cfg.Listen, fmt.Sprint(cfg.AgentPort)))
		return nil, truth.NoRecordings(upstream, agentURL)
	}
	var volatileFields []string
	if up, ok := cfg.Upstreams[upstream]; ok {
		volatileFields = up.VolatileFields
	}
	return truth.Score(def, jsonRecords, truth.Options{Seed: entry.Seed, Volatile: volatileFields, Limit: limit, Recordings: recordingsFor(cfg, entry), RecordingsMode: entry.Recordings})
}

func truthfulnessSummary(cfg *config.Config) map[string]any {
	out := map[string]any{}
	for _, upstream := range cfg.UpstreamNames() {
		report, err := truthfulnessFor(cfg, upstream, 200)
		if err != nil {
			continue
		}
		out[upstream] = map[string]any{"percent": report.Percent, "responses": report.Responses, "endpoints": report.Endpoints}
	}
	return out
}
