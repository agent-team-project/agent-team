package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/intake"
	"github.com/jamesaud/agent-team/internal/job"
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
	cmd.AddCommand(newIntakeServeCmd())
	return cmd
}

var intakeInput io.Reader = os.Stdin

func newIntakeLinearCmd() *cobra.Command {
	return newWebhookIntakeCmd("linear", intake.NormalizeLinear)
}

func newIntakeGitHubCmd() *cobra.Command {
	return newWebhookIntakeCmd("github", intake.NormalizeGitHub)
}

func newWebhookIntakeCmd(provider string, normalize func([]byte) (*intake.Event, error)) *cobra.Command {
	var (
		target        string
		payload       string
		payloadFile   string
		dryRun        bool
		previewRoutes bool
		reconcileJob  bool
		cleanupMerged bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   provider,
		Short: "Normalize a " + provider + " webhook payload and publish it.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: --format cannot be combined with --json.\n", provider)
				return exitErr(2)
			}
			if provider == "github" && cleanupMerged && !reconcileJob {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake github: --cleanup-merged requires --reconcile-job.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: --preview-triggers requires --dry-run.\n", provider)
				return exitErr(2)
			}
			tmpl, err := parseIntakeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
				return exitErr(2)
			}
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
			var reconcile *job.ReconcileResult
			var cleanupPreview *jobCleanupPreview
			var triggerPreview *eventPublishPreview
			cleanup := ""
			if (provider == "github" && reconcileJob) || previewRoutes {
				teamDir, err := resolveTeamDir(cmd, target)
				if err != nil {
					return err
				}
				if provider == "github" && reconcileJob {
					if dryRun {
						reconcile, cleanupPreview, err = previewGitHubIntakeJob(teamDir, ev, cleanupMerged)
					} else {
						if err := preflightIntakeDaemon(teamDir); err != nil {
							fmt.Fprintln(cmd.ErrOrStderr(), err)
							return exitErr(2)
						}
						reconcile, cleanup, err = reconcileGitHubIntakeJob(teamDir, ev, cleanupMerged)
					}
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake github: %v\n", err)
						return exitErr(1)
					}
				}
				if previewRoutes {
					triggerPreview, err = previewEventPublish(teamDir, ev.Type, ev.Payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
						return exitErr(1)
					}
				}
			}
			if dryRun {
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl, reconcile, cleanupPreview, triggerPreview)
			}
			return publishIntakeEventWithJob(cmd, target, ev, jsonOut, tmpl, reconcile, cleanup)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&payload, "payload", "", "Webhook JSON object.")
	cmd.Flags().StringVar(&payloadFile, "payload-file", "", "Read webhook JSON from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Normalize and print the event without publishing to the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	if provider == "github" {
		cmd.Flags().BoolVar(&reconcileJob, "reconcile-job", false, "Also reconcile the normalized PR event into the owning durable job.")
		cmd.Flags().BoolVar(&cleanupMerged, "cleanup-merged", false, "With --reconcile-job, remove the job-owned worktree and branch after a merged PR event.")
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit normalized event and daemon outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the intake result with a Go template, e.g. '{{.Event.Type}}'.")
	return cmd
}

func newIntakeScheduleCmd() *cobra.Command {
	var (
		target        string
		payload       string
		dryRun        bool
		previewRoutes bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "schedule <name>",
		Short: "Publish a named schedule event.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake schedule: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: %v\n", err)
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake schedule: --preview-triggers requires --dry-run.")
				return exitErr(2)
			}
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
				var triggerPreview *eventPublishPreview
				if previewRoutes {
					teamDir, err := resolveTeamDir(cmd, target)
					if err != nil {
						return err
					}
					triggerPreview, err = previewEventPublish(teamDir, ev.Type, ev.Payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: %v\n", err)
						return exitErr(1)
					}
				}
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl, nil, nil, triggerPreview)
			}
			return publishIntakeEvent(cmd, target, ev, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&payload, "payload", "", "Additional JSON object merged into the schedule payload.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Normalize and print the event without publishing to the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit normalized event and daemon outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the intake result with a Go template, e.g. '{{.Event.Type}}'.")
	return cmd
}

type intakeServeOptions struct {
	DryRun              bool
	PreviewTriggers     bool
	GitHubReconcileJob  bool
	GitHubCleanupMerged bool
	MaxBodyBytes        int64
}

func newIntakeServeCmd() *cobra.Command {
	var (
		target string
		addr   string
		opts   intakeServeOptions
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a local HTTP listener for external webhook intake.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.PreviewTriggers && !opts.DryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --preview-triggers requires --dry-run.")
				return exitErr(2)
			}
			if opts.GitHubCleanupMerged && !opts.GitHubReconcileJob {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --github-cleanup-merged requires --github-reconcile-job.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake serve: listen %s: %v\n", addr, err)
				return exitErr(1)
			}
			srv := &http.Server{
				Handler:           newIntakeServeHandler(teamDir, opts),
				ReadHeaderTimeout: 5 * time.Second,
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			errCh := make(chan error, 1)
			go func() {
				errCh <- srv.Serve(ln)
			}()
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake serve: listening on http://%s (POST /linear, POST /github, GET /healthz)\n", ln.Addr().String())
			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := srv.Shutdown(shutdownCtx); err != nil {
					return err
				}
				if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
				return nil
			case err := <-errCh:
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8787", "Address for the webhook listener.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Normalize requests and return previews without publishing to the daemon.")
	cmd.Flags().BoolVar(&opts.PreviewTriggers, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	cmd.Flags().BoolVar(&opts.GitHubReconcileJob, "github-reconcile-job", false, "For GitHub PR events, also reconcile the owning durable job.")
	cmd.Flags().BoolVar(&opts.GitHubCleanupMerged, "github-cleanup-merged", false, "With --github-reconcile-job, remove the job-owned worktree and branch after a merged PR event.")
	return cmd
}

func newIntakeServeHandler(teamDir string, opts intakeServeOptions) http.Handler {
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 1 << 20
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				writeIntakeServeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			writeIntakeServeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		case "/linear":
			handleIntakeServeWebhook(w, r, teamDir, "linear", intake.NormalizeLinear, opts)
		case "/github":
			handleIntakeServeWebhook(w, r, teamDir, "github", intake.NormalizeGitHub, opts)
		default:
			writeIntakeServeError(w, http.StatusNotFound, "unknown intake endpoint")
		}
	})
}

func handleIntakeServeWebhook(w http.ResponseWriter, r *http.Request, teamDir, provider string, normalize func([]byte) (*intake.Event, error), opts intakeServeOptions) {
	if r.Method != http.MethodPost {
		writeIntakeServeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, opts.MaxBodyBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeIntakeServeError(w, http.StatusRequestEntityTooLarge, "payload too large")
			return
		}
		writeIntakeServeError(w, http.StatusBadRequest, fmt.Sprintf("read request body: %v", err))
		return
	}
	ev, err := normalize(body)
	if err != nil {
		writeIntakeServeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, status, err := processIntakeServeEvent(teamDir, provider, ev, opts)
	if err != nil {
		writeIntakeServeError(w, status, err.Error())
		return
	}
	writeIntakeServeJSON(w, status, result)
}

func processIntakeServeEvent(teamDir, provider string, ev *intake.Event, opts intakeServeOptions) (*intakePublishResult, int, error) {
	if opts.DryRun {
		result := &intakePublishResult{Event: ev, DryRun: true}
		if provider == "github" && opts.GitHubReconcileJob {
			reconcile, cleanupPreview, err := previewGitHubIntakeJob(teamDir, ev, opts.GitHubCleanupMerged)
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
			result.Reconcile = reconcile
			result.CleanupPreview = cleanupPreview
		}
		if opts.PreviewTriggers {
			preview, err := previewEventPublish(teamDir, ev.Type, ev.Payload)
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
			result.Preview = preview
		}
		return result, http.StatusOK, nil
	}

	var reconcile *job.ReconcileResult
	cleanup := ""
	if provider == "github" && opts.GitHubReconcileJob {
		if err := preflightIntakeDaemon(teamDir); err != nil {
			return nil, http.StatusServiceUnavailable, err
		}
		var err error
		reconcile, cleanup, err = reconcileGitHubIntakeJob(teamDir, ev, opts.GitHubCleanupMerged)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			return nil, http.StatusServiceUnavailable, errors.New("agent-team intake: daemon is not running — start it first with `agent-team daemon start`")
		}
		return nil, http.StatusInternalServerError, err
	}
	outcome, err := dc.PublishEvent(ev.Type, ev.Payload)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return &intakePublishResult{Event: ev, Outcome: outcome, Reconcile: reconcile, Cleanup: cleanup}, http.StatusOK, nil
}

func writeIntakeServeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeIntakeServeError(w http.ResponseWriter, status int, message string) {
	writeIntakeServeJSON(w, status, map[string]string{"error": message})
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
	return readPayloadFile(payloadFile)
}

func readPayloadFile(payloadFile string) ([]byte, error) {
	if strings.TrimSpace(payloadFile) == "-" {
		body, err := io.ReadAll(intakeInput)
		if err != nil {
			return nil, fmt.Errorf("--payload-file -: %w", err)
		}
		return body, nil
	}
	body, err := os.ReadFile(filepath.Clean(payloadFile))
	if err != nil {
		return nil, fmt.Errorf("--payload-file: %w", err)
	}
	return body, nil
}

type intakePublishResult struct {
	Event          *intake.Event        `json:"event"`
	Outcome        *eventResponse       `json:"outcome"`
	Reconcile      *job.ReconcileResult `json:"reconcile,omitempty"`
	Cleanup        string               `json:"cleanup,omitempty"`
	CleanupPreview *jobCleanupPreview   `json:"cleanup_preview,omitempty"`
	Preview        *eventPublishPreview `json:"preview,omitempty"`
	DryRun         bool                 `json:"dry_run,omitempty"`
}

func parseIntakeFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("intake-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderIntakeDryRun(w io.Writer, ev *intake.Event, jsonOut bool, tmpl *template.Template, reconcile *job.ReconcileResult, cleanupPreview *jobCleanupPreview, triggerPreview *eventPublishPreview) error {
	result := intakePublishResult{Event: ev, Reconcile: reconcile, CleanupPreview: cleanupPreview, Preview: triggerPreview, DryRun: true}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderIntakeTemplate(w, result, tmpl)
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
	if reconcile != nil && reconcile.Job != nil {
		fmt.Fprintf(w, "Job: %s would reconcile by %s status=%s\n", reconcile.Job.ID, reconcile.MatchedBy, reconcile.Job.Status)
	}
	if cleanupPreview != nil {
		fmt.Fprintf(w, "Cleanup: %s\n", cleanupPreview.Summary)
	}
	if triggerPreview != nil {
		if !eventPublishPreviewHasRoutes(triggerPreview) {
			fmt.Fprintln(w, "Triggers: none")
		} else {
			return renderEventPublishRoutePreview(w, triggerPreview)
		}
	}
	return nil
}

func preflightIntakeDaemon(teamDir string) error {
	if _, err := newDaemonClient(teamDir); err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			return errors.New("agent-team intake: daemon is not running — start it first with `agent-team daemon start`.")
		}
		return err
	}
	return nil
}

func publishIntakeEvent(cmd *cobra.Command, target string, ev *intake.Event, jsonOut bool, tmpl *template.Template) error {
	return publishIntakeEventWithJob(cmd, target, ev, jsonOut, tmpl, nil, "")
}

func publishIntakeEventWithJob(cmd *cobra.Command, target string, ev *intake.Event, jsonOut bool, tmpl *template.Template, reconcile *job.ReconcileResult, cleanup string) error {
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
	result := intakePublishResult{Event: ev, Outcome: res, Reconcile: reconcile, Cleanup: cleanup}
	if jsonOut {
		return json.NewEncoder(out).Encode(result)
	}
	if tmpl != nil {
		return renderIntakeTemplate(out, result, tmpl)
	}
	fmt.Fprintf(out, "Event: %s\n", ev.Type)
	if err := renderIntakeOutcome(out, res); err != nil {
		return err
	}
	if reconcile != nil && reconcile.Job != nil {
		fmt.Fprintf(out, "Job: %s reconciled by %s status=%s\n", reconcile.Job.ID, reconcile.MatchedBy, reconcile.Job.Status)
	}
	if cleanup != "" {
		fmt.Fprintf(out, "Cleanup: %s\n", cleanup)
	}
	return nil
}

func reconcileGitHubIntakeJob(teamDir string, ev *intake.Event, cleanupMerged bool) (*job.ReconcileResult, string, error) {
	result, err := job.ReconcilePR(teamDir, job.ReconcileInputFromPayload(ev.Type, ev.Payload), time.Now().UTC())
	if err != nil {
		return nil, "", err
	}
	cleanup := ""
	if cleanupMerged && result.Job.Status == job.StatusDone {
		repoRoot := filepath.Dir(teamDir)
		cleanup, err = cleanupJobOwnedWorktree(repoRoot, result.Job)
		if err != nil {
			return nil, "", err
		}
		result.Job.Worktree = ""
		result.Job.Branch = ""
		result.Job.LastStatus = strings.TrimSpace(result.Job.LastStatus + "; cleanup: " + cleanup)
		result.Job.UpdatedAt = time.Now().UTC()
		if err := writeJobWithAudit(teamDir, result.Job, "cleanup", "cli", cleanup, nil); err != nil {
			return nil, "", err
		}
	}
	return result, cleanup, nil
}

func previewGitHubIntakeJob(teamDir string, ev *intake.Event, cleanupMerged bool) (*job.ReconcileResult, *jobCleanupPreview, error) {
	result, err := job.PreviewReconcilePR(teamDir, job.ReconcileInputFromPayload(ev.Type, ev.Payload), time.Now().UTC())
	if err != nil {
		return nil, nil, err
	}
	var cleanupPreview *jobCleanupPreview
	if cleanupMerged && result.Job.Status == job.StatusDone {
		preview, err := previewJobCleanup(filepath.Dir(teamDir), result.Job)
		if err != nil {
			return nil, nil, err
		}
		cleanupPreview = &preview
	}
	return result, cleanupPreview, nil
}

func renderIntakeTemplate(w io.Writer, result intakePublishResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
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
