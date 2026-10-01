package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var commandGroups = []struct{ id, title string }{
	{"sandbox", "Sandbox"},
	{"scenarios", "Scenarios"},
	{"ci", "CI"},
	{"observe", "Observe"},
	{"setup", "Setup"},
	{"more", "More"},
}

func inGroup(id string, c *cobra.Command) *cobra.Command {
	c.GroupID = id
	return c
}

func movedFrom(c *cobra.Command, old string) *cobra.Command {
	body := c.Long
	if body == "" {
		body = c.Short
	}
	c.Long = strings.TrimSpace(body) + "\n\nOld spelling: `" + old + "`. It still works this release and prints a notice on stderr."
	return c
}

func oldSpelling(c *cobra.Command, old, now string) *cobra.Command {
	c.Hidden = true
	c.Short = "old spelling of `" + now + "`"
	c.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		fmt.Fprintf(cmd.ErrOrStderr(), "`%s` is now `%s`; the old spelling still works this release\n", old, now)
	}
	return c
}

func agentSubcommands() []*cobra.Command {
	return []*cobra.Command{
		movedFrom(newIncidentsCmd(), "pikopod incidents"),
		movedFrom(newStatusCmd(), "pikopod status"),
		movedFrom(newReportCmd(), "pikopod report"),
		movedFrom(newInspectCmd(), "pikopod inspect"),
		movedFrom(newAckCmd(), "pikopod ack"),
		movedFrom(newAcceptCmd(), "pikopod accept"),
		movedFrom(newBaselineCmd(), "pikopod baseline"),
		movedFrom(newConformanceCmd(), "pikopod conformance"),
		movedFrom(newContractCmd(), "pikopod contract"),
		movedFrom(newSpecUpdateCmd(), "pikopod spec-update"),
		movedFrom(newVolatileCmd(), "pikopod volatile"),
		movedFrom(newReplayCmd(), "pikopod replay"),
		newTruthfulnessCmd(),
	}
}

func newAgentCmd() *cobra.Command {
	c := &cobra.Command{Use: "agent", Short: "The observing agent: incidents, status, baselines, conformance, contract, spec updates, replay"}
	c.AddCommand(agentSubcommands()...)
	return c
}

func oldTopLevelSpellings() []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range agentSubcommands() {
		name := sub.Name()
		out = append(out, oldSpelling(sub, "pikopod "+name, "pikopod agent "+name))
	}
	return append(out, oldSpelling(newWhyCmd(), "pikopod why", "pikopod requests <sandbox> --explain <METHOD> <path>"))
}
