package cli

import (
	"encoding/json"
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
	strict bool
	json   bool
}

type upgradeCheckResult struct {
	LockedRef        string `json:"locked_ref"`
	LockedTemplate   string `json:"locked_template,omitempty"`
	LockedVersion    string `json:"locked_version,omitempty"`
	LockedHash       string `json:"locked_hash"`
	TargetRef        string `json:"target_ref"`
	TargetTemplate   string `json:"target_template,omitempty"`
	TargetVersion    string `json:"target_version,omitempty"`
	TargetHash       string `json:"target_hash"`
	UpToDate         bool   `json:"up_to_date"`
	Differs          bool   `json:"differs"`
	ApplyImplemented bool   `json:"apply_implemented"`
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
	cmd.Flags().BoolVar(&cfg.strict, "strict", false, "With --check, exit 1 when the target template differs from the lock.")
	cmd.Flags().BoolVar(&cfg.json, "json", false, "Emit the upgrade check result as JSON.")
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

	result := upgradeCheckResult{
		LockedRef:        lock.Template.Ref,
		LockedTemplate:   lock.Template.Name,
		LockedVersion:    lock.Template.Version,
		LockedHash:       lock.Template.ContentHash,
		TargetRef:        targetRef,
		TargetHash:       targetHash,
		UpToDate:         targetHash == lock.Template.ContentHash,
		ApplyImplemented: false,
	}
	result.Differs = !result.UpToDate
	if rt.Manifest != nil {
		result.TargetTemplate = rt.Manifest.Template.Name
		result.TargetVersion = rt.Manifest.Template.Version
	}

	out := cmd.OutOrStdout()
	if cfg.json {
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return err
		}
	} else {
		renderUpgradeCheck(out, result)
	}
	if cfg.strict && result.Differs {
		return exitErr(1)
	}
	return nil
}

func renderUpgradeCheck(out fmtWriter, result upgradeCheckResult) {
	fmt.Fprintf(out, "Locked ref: %s\n", result.LockedRef)
	if result.LockedTemplate != "" || result.LockedVersion != "" {
		fmt.Fprintf(out, "Locked template: %s v%s\n", result.LockedTemplate, result.LockedVersion)
	}
	fmt.Fprintf(out, "Locked hash: %s\n", result.LockedHash)
	fmt.Fprintf(out, "Target ref: %s\n", result.TargetRef)
	if result.TargetTemplate != "" || result.TargetVersion != "" {
		fmt.Fprintf(out, "Target template: %s v%s\n", result.TargetTemplate, result.TargetVersion)
	}
	fmt.Fprintf(out, "Target hash: %s\n", result.TargetHash)
	if result.UpToDate {
		fmt.Fprintln(out, "agent-team upgrade: already up to date")
		return
	}
	fmt.Fprintln(out, "agent-team upgrade: template differs; full merge/apply is not implemented yet")
}
