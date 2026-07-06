package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newResolveVerbCmd is a hidden helper the runtime shim uses to resolve an
// invocation to its canonical dotted verb path using Cobra's own command tree.
//
// The shim MUST NOT maintain its own copy of the command tree — that copy
// drifts from reality (positional args, leaf verbs, aliases) and every drift is
// a wrongly-denied command. Instead the shim asks the real binary: this command
// runs Cobra's Find() over the same root the CLI dispatches from, so aliases
// (ls -> ps, top -> stats), nested subcommands, and positional arguments all
// resolve exactly as a real invocation would. Unknown commands exit non-zero.
//
// Output: the canonical verb path with segments joined by '.', e.g.
//	agent-team job merge squ-1   -> "job.merge"
//	agent-team ls                -> "ps"
//	agent-team run worker        -> "run"
func newResolveVerbCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "__resolve-verb [args...]",
		Hidden:             true,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			verb, ok := resolveVerbPath(cmd.Root(), args)
			if !ok {
				return fmt.Errorf("unknown verb")
			}
			fmt.Fprintln(cmd.OutOrStdout(), verb)
			return nil
		},
	}
}

// resolveVerbPath resolves args against root's command tree and returns the
// canonical dotted path of the matched command. Leading flags are skipped so
// `--repo X job merge` resolves the same as `job merge`. Returns ok=false when
// the args do not resolve to a real (non-root) command.
func resolveVerbPath(root *cobra.Command, args []string) (string, bool) {
	// Drop leading global flags and their values so the first positional is the
	// verb. DisableFlagParsing on the helper means we receive raw argv.
	positional := make([]string, 0, len(args))
	skipValue := false
	for _, a := range args {
		if skipValue {
			skipValue = false
			continue
		}
		if a == "--" {
			continue
		}
		if strings.HasPrefix(a, "--repo=") || strings.HasPrefix(a, "--target=") {
			continue
		}
		if a == "--repo" || a == "--target" {
			skipValue = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		positional = append(positional, a)
	}
	if len(positional) == 0 {
		return "", false
	}
	target, _, err := root.Find(positional)
	if err != nil || target == nil || target == root {
		return "", false
	}
	// The resolved command is the one Cobra will actually execute — that is what
	// the allowlist gates. An unknown subcommand under a real group (`job bogus`)
	// resolves to the group `job` (which just prints help); the dangerous leaf
	// `job merge` resolves to `job.merge`. A wholly-unknown top-level token
	// resolves to root above and is rejected. We deliberately do NOT replicate
	// any tree-shape rules here — Cobra's resolution is the single source.
	// Build the dotted path from root's children down to target.
	var segments []string
	for c := target; c != nil && c != root; c = c.Parent() {
		segments = append([]string{c.Name()}, segments...)
	}
	if len(segments) == 0 {
		return "", false
	}
	return strings.Join(segments, "."), true
}
