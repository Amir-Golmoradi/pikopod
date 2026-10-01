package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

func newRootCmd() *cobra.Command {
	cobra.EnableCommandSorting = false
	root := &cobra.Command{
		Use:           "pikopod",
		Short:         "Sandbox, scenario-test, and drift-watch your third-party API integrations — locally",
		Version:       currentVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().String("config", "", "path to pikopod.yaml (default: ./pikopod.yaml)")

	for _, g := range commandGroups {
		root.AddGroup(&cobra.Group{ID: g.id, Title: g.title})
	}

	root.AddCommand(
		inGroup("sandbox", newImportCmd()),
		inGroup("sandbox", newUpCmd()),
		inGroup("sandbox", newModeCmd()),
		inGroup("sandbox", newChaosCmd()),
		inGroup("sandbox", newRuleCmd()),
		inGroup("sandbox", newWebhookCmd()),
		inGroup("sandbox", newRequestsCmd()),
		inGroup("sandbox", newReproduceCmd()),
		inGroup("scenarios", newScenarioCmd()),
		inGroup("ci", newSpecDiffCmd()),
		inGroup("observe", newAgentCmd()),
		inGroup("setup", newInitCmd()),
		inGroup("setup", newDoctorCmd()),
		inGroup("setup", newDemoCmd()),
		inGroup("setup", newMCPCmd()),
		inGroup("more", newFixCmd()),
		inGroup("more", newPRCmd()),
		inGroup("more", newSandboxCmd()),
	)

	for _, old := range oldTopLevelSpellings() {
		root.AddCommand(old)
	}
	return root
}
