package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

func newPipelineOutboxCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
		all         bool
		sortBy      string
		limit       int
		summary     bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "outbox [<pipeline>|--all]",
		Short: "List or control pipeline-owned outbox events.",
		Long:  "List sandboxed agent outbox events owned by one pipeline. With no pipeline, all pipeline-owned outbox events are listed. Outbox subcommands still require an explicit pipeline.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: pass at most one pipeline name.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || cmd.Flags().Changed("limit")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: pipeline name is required.")
				return exitErr(2)
			}
			if summary {
				return runPipelineOutboxSummary(cmd.OutOrStdout(), teamDir, pipelineName, filters, jsonOut)
			}
			return runPipelineOutboxList(cmd.OutOrStdout(), teamDir, pipelineName, filters, outboxListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&all, "all", false, "List outbox events across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "Sort rows by state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate outbox counts instead of rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline-owned outbox rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.AddCommand(newPipelineOutboxShowCmd())
	cmd.AddCommand(newPipelineOutboxRetryCmd())
	cmd.AddCommand(newPipelineOutboxDropCmd())
	return cmd
}

func newPipelineOutboxShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <pipeline> <id>",
		Short: "Show one outbox event owned by one pipeline.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readPipelineOutboxItem(cmd, teamDir, args[0], args[1], "show")
			if err != nil {
				return err
			}
			return renderOutboxItemResult(cmd.OutOrStdout(), item, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the pipeline-owned outbox item as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newPipelineOutboxRetryCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		retryAll    bool
		dryRun      bool
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "retry <pipeline> [id]",
		Aliases: []string{"requeue"},
		Short:   "Retry outbox events owned by one pipeline.",
		Long:    "Move one pipeline-owned processed or failed outbox event back to pending by id, or retry a filtered pipeline-owned batch with --all. Batch retries default to failed events.",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: %v\n", err)
				return exitErr(2)
			}
			if retryAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --all requires exactly one pipeline and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.OutboxStateFailed
				}
				filters, err := parseOutboxFilters(effectiveState, types, sources, jobs)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: %v\n", err)
					return exitErr(2)
				}
				teamDir, err := resolveTeamDir(cmd, repo)
				if err != nil {
					return err
				}
				return runPipelineOutboxRetryAll(cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: requires <pipeline> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(types) > 0 || len(sources) > 0 || len(jobs) > 0 || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --state, --type, --source, --job, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if _, err := readPipelineOutboxItem(cmd, teamDir, args[0], args[1], "retry"); err != nil {
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
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching pipeline-owned outbox events instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the retry without moving the event.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newPipelineOutboxDropCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		dropAll     bool
		dryRun      bool
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <pipeline> [id]",
		Short: "Drop outbox events owned by one pipeline.",
		Long:  "Remove one pipeline-owned outbox event by id, or drop a filtered pipeline-owned batch with --all. Batch drops default to failed events.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: %v\n", err)
				return exitErr(2)
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --all requires exactly one pipeline and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.OutboxStateFailed
				}
				filters, err := parseOutboxFilters(effectiveState, types, sources, jobs)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: %v\n", err)
					return exitErr(2)
				}
				teamDir, err := resolveTeamDir(cmd, repo)
				if err != nil {
					return err
				}
				return runPipelineOutboxDropAll(cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: requires <pipeline> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(types) > 0 || len(sources) > 0 || len(jobs) > 0 || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --state, --type, --source, --job, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if _, err := readPipelineOutboxItem(cmd, teamDir, args[0], args[1], "drop"); err != nil {
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
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching pipeline-owned outbox events instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the drop without removing the event.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func runPipelineOutboxList(w io.Writer, teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template) error {
	items, err := collectPipelineOutboxItems(teamDir, pipeline)
	if err != nil {
		return err
	}
	return runOutboxListItems(w, items, filters, opts, jsonOut, tmpl)
}

func runPipelineOutboxSummary(w io.Writer, teamDir, pipeline string, filters outboxListFilters, jsonOut bool) error {
	items, err := collectPipelineOutboxItems(teamDir, pipeline)
	if err != nil {
		return err
	}
	return renderOutboxSummaryForItems(w, items, filters, jsonOut)
}

func runPipelineOutboxRetryAll(w io.Writer, teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, dryRun, jsonOut bool, tmpl *template.Template) error {
	matches, err := filteredPipelineOutboxItems(teamDir, pipeline, filters, opts)
	if err != nil {
		return err
	}
	results, err := retryOutboxItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderOutboxActionResults(w, results, jsonOut, tmpl)
}

func runPipelineOutboxDropAll(w io.Writer, teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, dryRun, jsonOut bool, tmpl *template.Template) error {
	matches, err := filteredPipelineOutboxItems(teamDir, pipeline, filters, opts)
	if err != nil {
		return err
	}
	results, err := dropOutboxItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderOutboxActionResults(w, results, jsonOut, tmpl)
}

func filteredPipelineOutboxItems(teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions) ([]*daemon.OutboxItem, error) {
	items, err := collectPipelineOutboxItems(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	return prepareOutboxActionMatches(filterOutboxItems(items, filters), opts), nil
}

func collectPipelineOutboxItems(teamDir, pipeline string) ([]*daemon.OutboxItem, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return nil, err
	}
	return outboxItemsForJobs(items, jobs), nil
}

func readPipelineOutboxItem(cmd *cobra.Command, teamDir, pipeline, id, verb string) (*daemon.OutboxItem, error) {
	item, err := daemon.ReadOutboxItem(teamDir, id)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox %s: outbox item %q not found.\n", verb, id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox %s: %v\n", verb, err)
		return nil, exitErr(1)
	}
	if len(outboxItemsForJobs([]*daemon.OutboxItem{item}, jobs)) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox %s: outbox item %q is not owned by pipeline %q.\n", verb, id, pipeline)
		return nil, exitErr(2)
	}
	return item, nil
}
