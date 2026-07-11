package tui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/daemonclient"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/outcomes"
	"github.com/agent-team-project/agent-team/internal/topology"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func TestSeededLiveDaemonDiscoveryAndOverviewParity(t *testing.T) {
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv("AGENT_TEAM_DAEMON_TOKEN_FILE", "")
	t.Setenv("AGENT_TEAM_DAEMON_SOCKET", "")
	harness := newSeededLiveDaemon(t)
	harness.start(t)

	client, err := daemonclient.New(harness.teamDir, daemonclient.Options{Timeout: 2 * time.Second, KeepAlive: true})
	if err != nil {
		t.Fatalf("zero-environment discovery: %v", err)
	}
	connection := client.Connection()
	if connection.Kind != daemonclient.TransportHTTP || connection.Endpoint != daemon.DaemonHTTPURL(harness.daemon.HTTPAddr()) {
		t.Fatalf("persisted HTTP did not win over live Unix socket: %+v", connection)
	}
	if connection.TokenFile != daemon.OperatorTokenPath(harness.teamDir) {
		t.Fatalf("default token file = %q, want %q", connection.TokenFile, daemon.OperatorTokenPath(harness.teamDir))
	}
	snapshot := client.Snapshot(context.Background(), fixtureTime)
	if !snapshot.Complete() {
		t.Fatalf("authenticated live snapshot errors = %v", snapshot.SourceErrors)
	}
	instances, err := client.Instances()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := client.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	topology, err := client.Topology()
	if err != nil {
		t.Fatal(err)
	}
	model := modelFromSnapshot(snapshot)
	projection := projectOverview(model)
	active := 0
	for _, job := range jobs {
		if job != nil && (job.Status == daemonclient.JobQueued || job.Status == daemonclient.JobRunning || job.Status == daemonclient.JobBlocked) {
			active++
		}
	}
	if projection.Summary.Instances != len(instances) || projection.Summary.Jobs != len(jobs) || projection.Summary.ActiveJobs != active || projection.Summary.Pipelines != len(topology.Pipelines) {
		t.Fatalf("typed TUI/API parity mismatch: projection=%+v instances=%d jobs=%d active=%d pipelines=%d", projection.Summary, len(instances), len(jobs), active, len(topology.Pipelines))
	}
	if projection.Summary.Instances != 6 || projection.Summary.Jobs != 12 || projection.Summary.ActiveJobs != 7 ||
		projection.Summary.ModelTiers != 4 || projection.Summary.BounceClasses != 4 || projection.Summary.Pipelines != 4 ||
		projection.Summary.Budgets != 2 || projection.Summary.Teams != 3 || projection.Summary.Schedules != 5 ||
		projection.Summary.Deployments != 1 || snapshot.DeploymentID != "tui-small-v1" {
		t.Fatalf("seeded tui-small-v1 projection = %+v deployment=%q", projection.Summary, snapshot.DeploymentID)
	}

	unauthenticated, err := http.Get(connection.Endpoint + "/v1/jobs")
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401 to prove the discovered token was required", unauthenticated.StatusCode)
	}

	if err := os.Remove(daemon.HTTPAddrPath(harness.teamDir)); err != nil {
		t.Fatal(err)
	}
	unixClient, err := daemonclient.New(harness.teamDir, daemonclient.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Unix fallback discovery: %v", err)
	}
	if got := unixClient.Connection(); got.Kind != daemonclient.TransportUnix || got.Endpoint != daemon.SocketPath(harness.teamDir) {
		t.Fatalf("Unix fallback connection = %+v", got)
	}
	if unixSnapshot := unixClient.Snapshot(context.Background(), fixtureTime); !unixSnapshot.Complete() || len(unixSnapshot.Jobs) != len(jobs) {
		t.Fatalf("Unix snapshot = complete %v jobs %d errors %v", unixSnapshot.Complete(), len(unixSnapshot.Jobs), unixSnapshot.SourceErrors)
	}

	client.CloseIdleConnections()
	unixClient.CloseIdleConnections()
	http.DefaultClient.CloseIdleConnections()
	harness.stop(t)
	if err := os.WriteFile(daemon.PidPath(harness.teamDir), []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := daemon.SetPidLiveCheckForTest(func(int) bool { return false })
	defer restore()
	if _, err := daemonclient.New(harness.teamDir, daemonclient.Options{}); !errors.Is(err, daemonclient.ErrNotRunning) {
		t.Fatalf("stale pidfile discovery error = %v, want ErrNotRunning", err)
	}
}

func TestPTYInducesRealDaemonDisconnectAndRecovery(t *testing.T) {
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv("AGENT_TEAM_DAEMON_TOKEN_FILE", "")
	t.Setenv("AGENT_TEAM_DAEMON_SOCKET", "")
	harness := newSeededLiveDaemon(t)
	harness.start(t)

	clockAt := fixtureTime
	runtime := &commandRuntime{ctx: context.Background(), teamDir: harness.teamDir, clock: func() time.Time { return clockAt }}
	domain := NewModel(clockAt, Capabilities{})
	domain.Polling = false
	testModel := teatest.NewTestModel(t, newProgramModel(domain, runtime), teatest.WithInitialTermSize(80, 24))
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool { return strings.Contains(string(output), "CONNECTED") }, teatest.WithDuration(5*time.Second))

	harness.stop(t)
	clockAt = clockAt.Add(time.Second)
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool { return strings.Contains(string(output), "DISCONNECTED") }, teatest.WithDuration(5*time.Second))

	harness.start(t)
	clockAt = clockAt.Add(time.Second)
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool { return strings.Contains(string(output), "RECONNECTED") }, teatest.WithDuration(5*time.Second))
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	final := testModel.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(ProgramModel)
	if final.Domain.Connection != ConnectionReconnected {
		t.Fatalf("final connection = %s, want reconnected", final.Domain.Connection)
	}
}

func modelFromSnapshot(snapshot *daemonclient.Snapshot) Model {
	model := NewModel(fixtureTime, Capabilities{})
	model.Booted = true
	model.RefreshInFlight = true
	for _, source := range daemonclient.SnapshotSources() {
		model, _ = Update(model, SnapshotOK{Source: source, Snapshot: snapshot, At: snapshot.SourceTimes[source]})
	}
	model, _ = Update(model, RefreshFinished{At: snapshot.CapturedAt, AnySuccess: true, Complete: snapshot.Complete()})
	return model
}

type seededLiveDaemon struct {
	root    string
	teamDir string
	daemon  *daemon.Daemon
	cancel  context.CancelFunc
}

func newSeededLiveDaemon(t *testing.T) *seededLiveDaemon {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "agt-tui-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLiveFile(t, filepath.Join(teamDir, "config.toml"), "[project]\nid = \"tui-small-v1\"\n")
	writeLiveFile(t, filepath.Join(teamDir, "instances.toml"), `[instances.frontend-worker]
agent = "worker"
ephemeral = true
replicas = 2

[instances.platform-worker]
agent = "worker"
ephemeral = true
replicas = 2

[instances.reviewer]
agent = "reviewer"
ephemeral = true
replicas = 2

[instances.verifier]
agent = "verifier"
ephemeral = true
replicas = 2

[instances.manager]
agent = "manager"

[instances.comms]
agent = "comms"

[instances.auditor]
agent = "auditor"

[instances.ticket-manager]
agent = "ticket-manager"

[pipelines.frontend_ticket_to_pr]
auto_advance = true
reap_worktree = "on_merge"
[pipelines.frontend_ticket_to_pr.trigger]
event = "agent.dispatch"

[[pipelines.frontend_ticket_to_pr.steps]]
id = "implement"
target = "frontend-worker"

[pipelines.platform]
reap_worktree = "on_merge"
[pipelines.platform.trigger]
event = "agent.dispatch"
[[pipelines.platform.steps]]
id = "implement"
target = "platform-worker"

[pipelines.release]
reap_worktree = "on_merge"
[pipelines.release.trigger]
event = "agent.dispatch"
[[pipelines.release.steps]]
id = "coordinate"
target = "manager"

[pipelines.quality]
reap_worktree = "on_merge"
[pipelines.quality.trigger]
event = "agent.dispatch"
[[pipelines.quality.steps]]
id = "review"
target = "reviewer"

[teams.frontend]
instances = ["frontend-worker", "reviewer"]
pipelines = ["frontend_ticket_to_pr"]

[teams.platform]
instances = ["platform-worker", "verifier", "manager"]
pipelines = ["platform"]

[teams.quality]
instances = ["comms", "auditor", "ticket-manager"]
pipelines = ["release", "quality"]

[budgets.frontend]
tokens_per_day = 40000000
jobs_in_flight = 2

[budgets.platform]
tokens_per_day = 80000000
jobs_in_flight = 2

[schedules.product-verify]
every = "24h"

[schedules.debt-sweep]
every = "24h"

[schedules.docs-freshness]
every = "24h"

[schedules.release]
every = "24h"

[schedules.feedback]
every = "24h"
`)
	exitCode := 1
	metadata := []*daemon.Metadata{
		{Instance: "frontend-worker-1", Agent: "worker", Status: daemon.StatusStopped, StartedAt: fixtureTime, Workspace: root},
		{Instance: "platform-worker-2", Agent: "worker", Status: daemon.StatusStopped, StartedAt: fixtureTime, Workspace: root},
		{Instance: "reviewer-gh382", Agent: "reviewer", Status: daemon.StatusStopped, StartedAt: fixtureTime, Workspace: root},
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped, StartedAt: fixtureTime, Workspace: root},
		{Instance: "verifier-2", Agent: "verifier", Status: daemon.StatusCrashed, StartedAt: fixtureTime, Workspace: root, ExitCode: &exitCode},
		{Instance: "comms", Agent: "comms", Status: daemon.StatusStopped, StartedAt: fixtureTime, Workspace: root},
	}
	for _, instance := range metadata {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), instance); err != nil {
			t.Fatal(err)
		}
	}
	statuses := []jobstore.Status{
		jobstore.StatusRunning, jobstore.StatusBlocked, jobstore.StatusFailed, jobstore.StatusQueued,
		jobstore.StatusDone, jobstore.StatusRunning, jobstore.StatusDone, jobstore.StatusBlocked,
		jobstore.StatusQueued, jobstore.StatusDone, jobstore.StatusRunning, jobstore.StatusDone,
	}
	classes := []string{"capability", "scope", "infra", "spec-ambiguity"}
	for i, status := range statuses {
		id := fmtJobID(i)
		job, err := jobstore.New("GH-"+fmtInt(380+i), "worker", "seeded tui-small-v1 job", fixtureTime.Add(-time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		job.ID = id
		job.Pipeline = "frontend_ticket_to_pr"
		job.Status = status
		job.Worktree = root
		job.UpdatedAt = fixtureTime.Add(-time.Duration(i) * time.Minute)
		if err := jobstore.Write(teamDir, job); err != nil {
			t.Fatal(err)
		}
		record := &outcomes.Record{JobID: id, Status: string(status), RecordedAt: fixtureTime}
		if i < 9 {
			record.Model = []string{"gpt-5.6", "gpt-5.5", "gpt-5.6"}[i%3]
			record.Tier = []string{"T2", "T1", "T3"}[i%3]
		}
		if i < len(classes) {
			record.BounceClasses = map[string]int{classes[i]: 1}
		}
		if err := outcomes.WriteRecord(teamDir, record); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := topology.LoadFromTeamDir(teamDir); err != nil {
		t.Fatalf("load seeded tui-small-v1 topology: %v", err)
	}
	return &seededLiveDaemon{root: root, teamDir: teamDir}
}

func (h *seededLiveDaemon) start(t *testing.T) {
	t.Helper()
	if h.daemon != nil {
		t.Fatal("seeded daemon already running")
	}
	d, err := daemon.New(daemon.Config{TeamDir: h.teamDir, LogOut: io.Discard, HTTPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.daemon, h.cancel = d, cancel
	go func() { _ = d.Run(ctx) }()
	t.Cleanup(func() {
		if h.daemon != nil {
			h.stop(t)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		addr, _ := daemon.ReadHTTPAddr(h.teamDir)
		if addr != "" {
			client := daemonclient.NewHTTP(daemon.DaemonHTTPURL(addr), daemon.OperatorTokenPath(h.teamDir), daemonclient.Options{Timeout: time.Second})
			if _, err := client.Status(); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("seeded daemon did not become ready: %s", h.teamDir)
}

func (h *seededLiveDaemon) stop(t *testing.T) {
	t.Helper()
	if h.daemon == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.daemon.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("shutdown seeded daemon: %v", err)
	}
	h.cancel()
	h.daemon = nil
	h.cancel = nil
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(daemon.SocketPath(h.teamDir)); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("seeded daemon socket remained after shutdown: %s", daemon.SocketPath(h.teamDir))
}

func writeLiveFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fmtJobID(index int) string {
	if index == 0 {
		return "gh383-tui-spec"
	}
	if index == 1 {
		return "release-2026-07"
	}
	return "job-" + fmtInt(index+1)
}

func fmtInt(value int) string {
	return strconv.Itoa(value)
}
