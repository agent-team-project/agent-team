package cli

import (
	"fmt"
	"io"
	"os"
	"text/template"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newTeamOutboxCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
		sortBy      string
		limit       int
		summary     bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "outbox <team>",
		Short: "List or control outbox events scoped to one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || cmd.Flags().Changed("limit")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if summary {
				return runTeamOutboxSummary(cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut)
			}
			return runTeamOutboxList(cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "Sort rows by state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate outbox counts instead of rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team-owned outbox rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each team-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.AddCommand(newTeamOutboxShowCmd())
	cmd.AddCommand(newTeamOutboxRetryCmd())
	cmd.AddCommand(newTeamOutboxDropCmd())
	return cmd
}

func newTeamOutboxShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <team> <id>",
		Short: "Show one outbox event owned by one team.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readTeamOutboxItem(cmd, teamDir, args[0], args[1], "show")
			if err != nil {
				return err
			}
			return renderOutboxItemResult(cmd.OutOrStdout(), item, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the team-owned outbox item as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newTeamOutboxRetryCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
		dryRun  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "retry <team> <id>",
		Aliases: []string{"requeue"},
		Short:   "Move one team-owned processed or failed outbox event back to pending.",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox retry: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if _, err := readTeamOutboxItem(cmd, teamDir, args[0], args[1], "retry"); err != nil {
				return err
			}
			result, err := retryOutboxItem(teamDir, args[1], dryRun)
			if err != nil {
				return err
			}
			return renderOutboxActionResults(cmd.OutOrStdout(), []outboxActionResult{result}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the retry without moving the event.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newTeamOutboxDropCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
		dryRun  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <team> <id>",
		Short: "Remove one team-owned outbox event.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if _, err := readTeamOutboxItem(cmd, teamDir, args[0], args[1], "drop"); err != nil {
				return err
			}
			result, err := dropOutboxItem(teamDir, args[1], dryRun)
			if err != nil {
				return err
			}
			return renderOutboxActionResults(cmd.OutOrStdout(), []outboxActionResult{result}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the drop without removing the event.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func runTeamOutboxList(w io.Writer, teamDir, name string, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template) error {
	items, err := collectTeamOutboxItems(teamDir, name)
	if err != nil {
		return err
	}
	return runOutboxListItems(w, items, filters, opts, jsonOut, tmpl)
}

func runTeamOutboxSummary(w io.Writer, teamDir, name string, filters outboxListFilters, jsonOut bool) error {
	items, err := collectTeamOutboxItems(teamDir, name)
	if err != nil {
		return err
	}
	return renderOutboxSummaryForItems(w, items, filters, jsonOut)
}

func collectTeamOutboxItems(teamDir, name string) ([]*daemon.OutboxItem, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return nil, err
	}
	return teamOutboxItems(top, team, teamJobs(top, team, jobs), items), nil
}

func readTeamOutboxItem(cmd *cobra.Command, teamDir, name, id, verb string) (*daemon.OutboxItem, error) {
	item, err := daemon.ReadOutboxItem(teamDir, id)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox %s: outbox item %q not found.\n", verb, id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox %s: %v\n", verb, err)
		return nil, exitErr(1)
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	if len(teamOutboxItems(top, team, teamJobs(top, team, jobs), []*daemon.OutboxItem{item})) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox %s: outbox item %q is not owned by team %q.\n", verb, id, name)
		return nil, exitErr(2)
	}
	return item, nil
}
