package main

import (
	"github.com/pikopod/pikopod/internal/config"
	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/pikopod/pikopod/internal/sandbox"
)

func divergenceFork(cfg *config.Config, upstream string) (*sandbox.Engine, func(), error) {
	entries, err := loadRegistry(cfg.DataDir)
	if err != nil {
		return nil, nil, err
	}
	var entry *sandboxEntry
	for i := range entries {
		if entries[i].Upstream == upstream || entries[i].Name == upstream {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return nil, nil, errfmt.New("no sandbox linked to "+upstream, "divergence compares the provider's answers with the sandbox built from its spec", "import one: `pikopod import "+upstream+" --spec …`", "docs/config-reference.md#data_dir")
	}
	_, def, err := loadSandboxDef(cfg, entry.Name)
	if err != nil {
		return nil, nil, err
	}
	rules, err := rulesFor(cfg, entry)
	if err != nil {
		return nil, nil, err
	}
	st, err := sandbox.OpenMemoryStore()
	if err != nil {
		return nil, nil, err
	}
	eng, err := sandbox.NewEngine(def, sandbox.Config{
		ID: entry.ID + "_divergence", Seed: entry.Seed, Mode: "deterministic", VirtualClockMs: entry.CreatedClockMs,
		Effective: effectiveFor(cfg, entry, 0), Rules: rules, RecordingsMode: "off",
	}, st)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	return eng, func() { eng.Close(); st.Close() }, nil
}
