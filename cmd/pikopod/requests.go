package main

import (
	"fmt"
	"strings"

	"github.com/pikopod/pikopod/internal/errfmt"
	"github.com/spf13/cobra"
)

func newRequestsCmd() *cobra.Command {
	journal := newSandboxRequestsCmd()
	c := &cobra.Command{Use: "requests <sandbox> [--explain <METHOD> <path>]",
		Short: "Show what a running sandbox received, or replay one request with decision tracing (--explain)",
		Long: `Show the requests a running sandbox has received: the client-behaviour journal.

With --explain, replay one request in-process against a fork of the sandbox
with the narrator on, and see exactly why it answers what it answers. No real
state is touched and no server is needed for --explain.

Old spellings: ` + "`pikopod sandbox requests <sandbox>`" + ` and ` + "`pikopod why <sandbox> <METHOD> <path>`" + `.
Both still work this release and print a notice on stderr.`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			explain, _ := cmd.Flags().GetBool("explain")
			if !explain {
				if len(args) != 1 {
					return errfmt.New("requests takes one sandbox", fmt.Sprintf("%d arguments were given", len(args)), "pass --explain <METHOD> <path> to replay one request", "")
				}
				return journal.RunE(cmd, args)
			}
			if len(args) != 3 {
				return errfmt.New("--explain needs a method and a path", "e.g. pikopod requests "+args[0]+" --explain GET /charges/ch_123", "pass the sandbox, then METHOD and a provider-relative path", "")
			}
			body, _ := cmd.Flags().GetString("body")
			withAuth, _ := cmd.Flags().GetBool("auth")
			return explainRequest(cmd, args[0], strings.ToUpper(args[1]), args[2], body, withAuth)
		}}
	c.Flags().AddFlagSet(journal.Flags())
	c.Flags().Bool("explain", false, "replay <METHOD> <path> against a fork of the sandbox with decision tracing")
	c.Flags().String("body", "", "JSON request body for --explain")
	c.Flags().Bool("auth", true, "with --explain, send the sandbox's issued credential (pass --auth=false to see the auth refusal path)")
	return c
}
