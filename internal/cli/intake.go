package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/jamesaud/agent-team/internal/intake"
	"github.com/spf13/cobra"
)

func newIntakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "intake",
		Short: "Normalize external events into topology events.",
		Long:  "Normalize external events such as Linear/GitHub webhooks and schedules into topology events handled by the daemon.",
	}
	cmd.AddCommand(newIntakeLinearCmd())
	cmd.AddCommand(newIntakeGitHubCmd())
	cmd.AddCommand(newIntakeScheduleCmd())
	return cmd
}

func newIntakeLinearCmd() *cobra.Command {
	return newWebhookIntakeCmd("linear", intake.NormalizeLinear)
}

func newIntakeGitHubCmd() *cobra.Command {
	return newWebhookIntakeCmd("github", intake.NormalizeGitHub)
}

func newWebhookIntakeCmd(provider string, normalize func([]byte) (*intake.Event, error)) *cobra.Command {
	var (
		target      string
		payload     string
		payloadFile string
		dryRun      bool
		jsonOut     bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   provider,
		Short: "Normalize a " + provider + " webhook payload and publish it.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := intakePayload(payload, payloadFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
				return exitErr(2)
			}
			ev, err := normalize(body)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
				return exitErr(2)
			}
			if dryRun {
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut)
			}
			return publishIntakeEvent(cmd, target, ev, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&payload, "payload", "", "Webhook JSON object.")
	cmd.Flags().StringVar(&payloadFile, "payload-file", "", "Read webhook JSON from a file.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Normalize and print the event without publishing to the daemon.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit normalized event and daemon outcome as JSON.")
	return cmd
}

func newIntakeScheduleCmd() *cobra.Command {
	var (
		target  string
		payload string
		dryRun  bool
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "schedule <name>",
		Short: "Publish a named schedule event.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{"source": "schedule", "name": args[0]}
			if strings.TrimSpace(payload) != "" {
				if err := json.Unmarshal([]byte(payload), &body); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: --payload is not valid JSON: %v\n", err)
					return exitErr(2)
				}
				body["source"] = "schedule"
				body["name"] = args[0]
			}
			ev := &intake.Event{Type: "schedule", Payload: body}
			if dryRun {
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut)
			}
			return publishIntakeEvent(cmd, target, ev, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&payload, "payload", "", "Additional JSON object merged into the schedule payload.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Normalize and print the event without publishing to the daemon.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit normalized event and daemon outcome as JSON.")
	return cmd
}

func intakePayload(payload, payloadFile string) ([]byte, error) {
	hasPayload := strings.TrimSpace(payload) != ""
	hasFile := strings.TrimSpace(payloadFile) != ""
	if hasPayload == hasFile {
		return nil, fmt.Errorf("provide exactly one of --payload or --payload-file")
	}
	if hasPayload {
		return []byte(payload), nil
	}
	body, err := os.ReadFile(filepath.Clean(payloadFile))
	if err != nil {
		return nil, fmt.Errorf("--payload-file: %w", err)
	}
	return body, nil
}

type intakePublishResult struct {
	Event   *intake.Event  `json:"event"`
	Outcome *eventResponse `json:"outcome"`
	DryRun  bool           `json:"dry_run,omitempty"`
}

func renderIntakeDryRun(w io.Writer, ev *intake.Event, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(intakePublishResult{Event: ev, DryRun: true})
	}
	fmt.Fprintf(w, "Event: %s\n", ev.Type)
	if len(ev.Payload) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE")
	keys := make([]string, 0, len(ev.Payload))
	for key := range ev.Payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(tw, "%s\t%v\n", key, ev.Payload[key])
	}
	_ = tw.Flush()
	return nil
}

func publishIntakeEvent(cmd *cobra.Command, target string, ev *intake.Event, jsonOut bool) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake: daemon is not running — start it first with `agent-team daemon start`.")
		return exitErr(2)
	}
	res, err := dc.PublishEvent(ev.Type, ev.Payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake: %v\n", err)
		return exitErr(1)
	}
	out := cmd.OutOrStdout()
	if jsonOut {
		return json.NewEncoder(out).Encode(intakePublishResult{Event: ev, Outcome: res})
	}
	fmt.Fprintf(out, "Event: %s\n", ev.Type)
	return renderIntakeOutcome(out, res)
}

func renderIntakeOutcome(w io.Writer, res *eventResponse) error {
	if len(res.Matched) == 0 {
		_, err := fmt.Fprintln(w, "(no triggers matched)")
		return err
	}
	fmt.Fprintf(w, "Matched: %s\n", strings.Join(res.Matched, ", "))
	for _, d := range res.Dispatched {
		name, _ := d["instance"].(string)
		id, _ := d["instance_id"].(string)
		fmt.Fprintf(w, "  dispatched %s as %s\n", name, id)
	}
	for _, n := range res.Queued {
		fmt.Fprintf(w, "  queued %s (at replica capacity)\n", n)
	}
	for _, n := range res.Messaged {
		fmt.Fprintf(w, "  messaged %s\n", n)
	}
	for _, r := range res.Rejected {
		name, _ := r["instance"].(string)
		reason, _ := r["reason"].(string)
		fmt.Fprintf(w, "  rejected %s: %s\n", name, reason)
	}
	return nil
}
