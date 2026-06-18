package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jamesaud/agent-team/internal/template"
	"github.com/spf13/cobra"
)

type upgradeConfig struct {
	target string
	toRef  string
	check  bool
}

func newUpgradeCmd() *cobra.Command {
	var cfg upgradeConfig
	cwd, _ := os.Getwd()

	cmd := &cobra.Command{
		Use:   "upgrade --check [--to <ref>]",
		Short: "Check whether the current repo's template lock matches a template ref.",
		Long: "Read-only upgrade preflight. Compares .agent_team/.template.lock against the locked " +
			"template ref, or --to <ref> when supplied. Full three-way upgrade/apply is not implemented yet.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cfg.check {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: upgrade apply is not implemented yet; pass --check for a read-only template comparison.")
				return exitErr(2)
			}
			return runUpgradeCheck(cmd, cfg)
		},
	}
	cfg.target = cwd
	cmd.Flags().StringVar(&cfg.target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&cfg.toRef, "to", "", "Template ref to compare against (defaults to the ref in .template.lock).")
	cmd.Flags().BoolVar(&cfg.check, "check", false, "Compare current template lock against a resolved template ref without writing files.")
	return cmd
}

func runUpgradeCheck(cmd *cobra.Command, cfg upgradeConfig) error {
	teamDir, err := resolveTeamDir(cmd, cfg.target)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(teamDir, template.LockFileName)
	lock, err := template.LoadLock(lockPath)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: cannot read %s: %v\n", lockPath, err)
		return exitErr(2)
	}

	targetRef := cfg.toRef
	if targetRef == "" {
		targetRef = lock.Template.Ref
	}
	resolver := newResolver()
	rt, err := resolver.Resolve(targetRef)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	targetHash, err := template.ContentHash(rt)
	if err != nil {
		return fmt.Errorf("hash template source: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Locked ref: %s\n", lock.Template.Ref)
	if lock.Template.Name != "" || lock.Template.Version != "" {
		fmt.Fprintf(out, "Locked template: %s v%s\n", lock.Template.Name, lock.Template.Version)
	}
	fmt.Fprintf(out, "Locked hash: %s\n", lock.Template.ContentHash)
	fmt.Fprintf(out, "Target ref: %s\n", targetRef)
	if rt.Manifest != nil {
		fmt.Fprintf(out, "Target template: %s v%s\n", rt.Manifest.Template.Name, rt.Manifest.Template.Version)
	}
	fmt.Fprintf(out, "Target hash: %s\n", targetHash)

	if targetHash == lock.Template.ContentHash {
		fmt.Fprintln(out, "agent-team upgrade: already up to date")
		return nil
	}
	fmt.Fprintln(out, "agent-team upgrade: template differs; full merge/apply is not implemented yet")
	return nil
}
