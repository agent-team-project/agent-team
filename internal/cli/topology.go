package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jamesaud/agent-team/internal/topology"
)

// newTopologyCmd registers the `topology` group: read-only inspection of the
// declared topology plus an explicit `reload`. Uses the daemon's
// /v1/topology endpoints when running; falls back to local file parsing so
// `agent-team topology` is useful even before the daemon is started.
func newTopologyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "topology",
		Short: "Show declared instances and triggers (reads .agent_team/instances.toml).",
	}
	cmd.AddCommand(newTopologyShowCmd())
	cmd.AddCommand(newTopologySummaryCmd())
	cmd.AddCommand(newTopologyReloadCmd())
	return cmd
}

func newTopologyShowCmd() *cobra.Command {
	var (
		target string
		asJSON bool
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the resolved topology (declared instances + triggers).",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runTopologyShow(cmd, teamDir, asJSON)
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().BoolVar(&asJSON, "json", false, "Emit raw JSON.")
	return c
}

func newTopologySummaryCmd() *cobra.Command {
	var (
		target string
		asJSON bool
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "summary",
		Short: "Summarize declared topology and workflow health.",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			summary, err := collectTopologySummary(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team topology summary: %v\n", err)
				return exitErr(1)
			}
			return renderTopologySummary(cmd.OutOrStdout(), summary, asJSON)
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().BoolVar(&asJSON, "json", false, "Emit topology summary as JSON.")
	return c
}

func newTopologyReloadCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "reload",
		Short: "Re-read instances.toml from disk (daemon must be running).",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running — start it first with `agent-team daemon start`.")
				return exitErr(2)
			}
			res, err := dc.TopologyReload()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(1)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Reloaded — %d instance(s) declared.\n", len(res.Instances))
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return c
}

// runTopologyShow prints either the daemon's view (if running, includes
// runtime running/queued counts) or a file-only view.
func runTopologyShow(cmd *cobra.Command, teamDir string, asJSON bool) error {
	// Prefer daemon-sourced topology — it includes per-instance running
	// counters. Fall back to parsing instances.toml ourselves so the command
	// is useful before the daemon is started.
	if dc, err := newDaemonClient(teamDir); err == nil {
		res, err := dc.Topology()
		if err == nil {
			if asJSON {
				body, _ := json.MarshalIndent(res, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(body))
				return nil
			}
			printDaemonTopology(cmd.OutOrStdout(), res)
			return nil
		}
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(1)
	}
	if top == nil || len(top.Instances) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances declared — add .agent_team/instances.toml)")
		return nil
	}
	if asJSON {
		// Mirror the daemon shape so consumers don't branch.
		body, _ := json.MarshalIndent(toResponseLike(top), "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(body))
		return nil
	}
	printLocalTopology(cmd.OutOrStdout(), top)
	return nil
}

type topologySummary struct {
	OK               bool `json:"ok"`
	Instances        int  `json:"instances"`
	Persistent       int  `json:"persistent"`
	Ephemeral        int  `json:"ephemeral"`
	Triggers         int  `json:"triggers"`
	Pipelines        int  `json:"pipelines"`
	PipelineSteps    int  `json:"pipeline_steps"`
	PipelineProblems int  `json:"pipeline_problems"`
	PipelineWarnings int  `json:"pipeline_warnings"`
	Schedules        int  `json:"schedules"`
	Teams            int  `json:"teams"`
	TeamProblems     int  `json:"team_problems"`
	TeamWarnings     int  `json:"team_warnings"`
}

func collectTopologySummary(teamDir string) (*topologySummary, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	summary := &topologySummary{OK: true}
	if top == nil {
		return summary, nil
	}
	summary.Instances = len(top.Instances)
	for _, inst := range top.SortedInstances() {
		if inst == nil {
			continue
		}
		if inst.Ephemeral {
			summary.Ephemeral++
		} else {
			summary.Persistent++
		}
		summary.Triggers += len(inst.Triggers)
	}
	summary.Pipelines = len(top.Pipelines)
	for _, pipeline := range top.SortedPipelines() {
		if pipeline == nil {
			continue
		}
		summary.PipelineSteps += len(pipeline.Steps)
	}
	summary.Schedules = len(top.Schedules)
	summary.Teams = len(top.Teams)
	if pipelineDoctor, err := collectPipelineDoctor(teamDir, ""); err != nil {
		return nil, err
	} else if pipelineDoctor != nil {
		summary.PipelineProblems = len(pipelineDoctor.Problems)
		summary.PipelineWarnings = countPipelineDoctorWarnings(pipelineDoctor)
	}
	if teamDoctor, err := collectAllTeamDoctor(teamDir); err != nil {
		return nil, err
	} else if teamDoctor != nil {
		summary.TeamProblems = len(teamDoctor.Problems)
		summary.TeamWarnings = countSnapshotTeamDoctorWarnings(teamDoctor.Warnings)
	}
	summary.OK = summary.PipelineProblems == 0 && summary.TeamProblems == 0
	return summary, nil
}

func renderTopologySummary(w io.Writer, summary *topologySummary, asJSON bool) error {
	if summary == nil {
		summary = &topologySummary{OK: true}
	}
	if asJSON {
		return json.NewEncoder(w).Encode(summary)
	}
	state := "ok"
	if !summary.OK {
		state = "attention"
	}
	fmt.Fprintf(w, "topology: %s\n", state)
	fmt.Fprintf(w, "instances: total=%d persistent=%d ephemeral=%d triggers=%d\n",
		summary.Instances,
		summary.Persistent,
		summary.Ephemeral,
		summary.Triggers)
	fmt.Fprintf(w, "pipelines: total=%d steps=%d problems=%d warnings=%d\n",
		summary.Pipelines,
		summary.PipelineSteps,
		summary.PipelineProblems,
		summary.PipelineWarnings)
	fmt.Fprintf(w, "schedules: total=%d\n", summary.Schedules)
	fmt.Fprintf(w, "teams: total=%d problems=%d warnings=%d\n",
		summary.Teams,
		summary.TeamProblems,
		summary.TeamWarnings)
	return nil
}

func toResponseLike(top *topology.Topology) map[string]any {
	out := make([]map[string]any, 0, len(top.Instances))
	for _, inst := range top.SortedInstances() {
		out = append(out, map[string]any{
			"name":        inst.Name,
			"agent":       inst.Agent,
			"ephemeral":   inst.Ephemeral,
			"description": inst.Description,
			"replicas":    inst.Replicas,
			"config":      map[string]any(inst.Config),
			"triggers":    triggersAsMaps(inst.Triggers),
		})
	}
	pipelines := make([]map[string]any, 0, len(top.Pipelines))
	for _, pipeline := range top.SortedPipelines() {
		pipelines = append(pipelines, map[string]any{
			"name":    pipeline.Name,
			"trigger": triggerAsMap(pipeline.Trigger),
			"steps":   pipelineStepsAsMaps(pipeline.Steps),
		})
	}
	schedules := make([]map[string]any, 0, len(top.Schedules))
	for _, schedule := range top.SortedSchedules() {
		schedules = append(schedules, map[string]any{
			"name":         schedule.Name,
			"every":        schedule.Every.String(),
			"run_on_start": schedule.RunOnStart,
			"payload":      schedule.Payload,
		})
	}
	return map[string]any{"instances": out, "pipelines": pipelines, "schedules": schedules}
}

func triggersAsMaps(triggers []*topology.Trigger) []map[string]any {
	out := make([]map[string]any, 0, len(triggers))
	for _, t := range triggers {
		match := map[string]any{}
		for k, mv := range t.Match {
			if mv.Single != "" {
				match[k] = mv.Single
			} else if len(mv.List) > 0 {
				match[k] = mv.List
			}
		}
		out = append(out, map[string]any{"event": t.Event, "match": match})
	}
	return out
}

func triggerAsMap(t *topology.Trigger) map[string]any {
	if t == nil {
		return nil
	}
	match := map[string]any{}
	for k, mv := range t.Match {
		if mv.Single != "" {
			match[k] = mv.Single
		} else if len(mv.List) > 0 {
			match[k] = mv.List
		}
	}
	return map[string]any{"event": t.Event, "match": match}
}

func pipelineStepsAsMaps(steps []*topology.PipelineStep) []map[string]any {
	out := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		out = append(out, map[string]any{"id": step.ID, "target": step.Target, "after": step.After})
	}
	return out
}

func printDaemonTopology(w io.Writer, res *topologyResponse) {
	if len(res.Instances) == 0 && len(res.Pipelines) == 0 && len(res.Schedules) == 0 {
		fmt.Fprintln(w, "(no topology declared)")
		return
	}
	if len(res.Instances) > 0 {
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tAGENT\tEPHEMERAL\tREPLICAS\tTRIGGERS\tRUNNING\tQUEUED")
		for _, i := range res.Instances {
			eph := "no"
			if i.Ephemeral {
				eph = "yes"
			}
			trigSummary := summariseTriggers(i.Triggers)
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%d\t%d\n",
				i.Name, i.Agent, eph, i.Replicas, trigSummary, i.Running, i.Queued)
		}
		_ = tw.Flush()
	}
	if len(res.Pipelines) > 0 {
		if len(res.Instances) > 0 {
			fmt.Fprintln(w)
		}
		printDaemonPipelines(w, res.Pipelines)
	}
	if len(res.Schedules) > 0 {
		if len(res.Instances) > 0 || len(res.Pipelines) > 0 {
			fmt.Fprintln(w)
		}
		printDaemonSchedules(w, res.Schedules)
	}
}

func printLocalTopology(w io.Writer, top *topology.Topology) {
	if len(top.Instances) > 0 {
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tAGENT\tEPHEMERAL\tREPLICAS\tTRIGGERS")
		for _, inst := range top.SortedInstances() {
			eph := "no"
			if inst.Ephemeral {
				eph = "yes"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
				inst.Name, inst.Agent, eph, inst.Replicas, summariseLocalTriggers(inst.Triggers))
		}
		_ = tw.Flush()
	}
	if len(top.Pipelines) > 0 {
		if len(top.Instances) > 0 {
			fmt.Fprintln(w)
		}
		printLocalPipelines(w, top.SortedPipelines())
	}
	if len(top.Schedules) > 0 {
		if len(top.Instances) > 0 || len(top.Pipelines) > 0 {
			fmt.Fprintln(w)
		}
		printLocalSchedules(w, top.SortedSchedules())
	}
}

func printDaemonPipelines(w io.Writer, pipelines []topologyPipeline) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tTRIGGER\tSTEPS")
	for _, pipeline := range pipelines {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", pipeline.Name, summariseTriggerMap(pipeline.Trigger), summarisePipelineStepMaps(pipeline.Steps))
	}
	_ = tw.Flush()
}

func printLocalPipelines(w io.Writer, pipelines []*topology.Pipeline) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tTRIGGER\tSTEPS")
	for _, pipeline := range pipelines {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", pipeline.Name, summariseLocalTrigger(pipeline.Trigger), summariseLocalPipelineSteps(pipeline.Steps))
	}
	_ = tw.Flush()
}

func printDaemonSchedules(w io.Writer, schedules []topologySchedule) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCHEDULE\tEVERY\tRUN_ON_START\tPAYLOAD")
	for _, schedule := range schedules {
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", schedule.Name, schedule.Every, schedule.RunOnStart, summarisePayloadMap(schedule.Payload))
	}
	_ = tw.Flush()
}

func printLocalSchedules(w io.Writer, schedules []*topology.Schedule) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCHEDULE\tEVERY\tRUN_ON_START\tPAYLOAD")
	for _, schedule := range schedules {
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", schedule.Name, schedule.Every, schedule.RunOnStart, summariseAnyPayloadMap(schedule.Payload))
	}
	_ = tw.Flush()
}

func summariseTriggers(triggers []map[string]interface{}) string {
	if len(triggers) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(triggers))
	for _, t := range triggers {
		ev, _ := t["event"].(string)
		match, _ := t["match"].(map[string]interface{})
		if len(match) > 0 {
			keys := make([]string, 0, len(match))
			for k := range match {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts = append(parts, fmt.Sprintf("%s(%s)", ev, strings.Join(keys, ",")))
		} else {
			parts = append(parts, ev)
		}
	}
	return strings.Join(parts, ", ")
}

func summarisePayloadMap(payload map[string]interface{}) string {
	if len(payload) == 0 {
		return "—"
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func summariseAnyPayloadMap(payload map[string]any) string {
	if len(payload) == 0 {
		return "—"
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func summariseLocalTriggers(triggers []*topology.Trigger) string {
	if len(triggers) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(triggers))
	for _, t := range triggers {
		if len(t.Match) > 0 {
			keys := make([]string, 0, len(t.Match))
			for k := range t.Match {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts = append(parts, fmt.Sprintf("%s(%s)", t.Event, strings.Join(keys, ",")))
		} else {
			parts = append(parts, t.Event)
		}
	}
	return strings.Join(parts, ", ")
}

func summariseTriggerMap(trigger map[string]interface{}) string {
	if len(trigger) == 0 {
		return "—"
	}
	event, _ := trigger["event"].(string)
	match, _ := trigger["match"].(map[string]interface{})
	if len(match) == 0 {
		return event
	}
	keys := make([]string, 0, len(match))
	for key := range match {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s(%s)", event, strings.Join(keys, ","))
}

func summariseLocalTrigger(trigger *topology.Trigger) string {
	if trigger == nil {
		return "—"
	}
	if len(trigger.Match) == 0 {
		return trigger.Event
	}
	keys := make([]string, 0, len(trigger.Match))
	for key := range trigger.Match {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s(%s)", trigger.Event, strings.Join(keys, ","))
}

func summarisePipelineStepMaps(steps []map[string]interface{}) string {
	if len(steps) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		id, _ := step["id"].(string)
		target, _ := step["target"].(string)
		parts = append(parts, id+"→"+target)
	}
	return strings.Join(parts, ", ")
}

func summariseLocalPipelineSteps(steps []*topology.PipelineStep) string {
	if len(steps) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		parts = append(parts, step.ID+"→"+step.Target)
	}
	return strings.Join(parts, ", ")
}
