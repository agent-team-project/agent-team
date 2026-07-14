package daemon

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
)

func TestActivationRejectsStaleCLIForScheduledTeamAuthorityBeforeSpawn(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "schedule"
match.name = "activation-check"

[schedules.activation-check]
every = "1h"
run_on_start = true

[schedules.activation-check.payload]
kind = "activation"

[teams.platform]
instances = ["worker"]
schedules = ["activation-check"]

[authority]
enforcement = "enforce"

[authority.instances.worker]
allow = ["job.gate.*:team"]
`)
	fake := newFakeSpawner(100 * time.Millisecond)
	mgr := NewInstanceManager(t.TempDir(), fake.spawn)
	resolver := NewEventResolver(mgr, teamDir, top)
	setActivationForTest(resolver, ActivationStatus{
		State:   ActivationStateNeeded,
		CLI:     buildinfo.Info{Version: "0.1.0", Revision: "b062047f11111111111111111111111111111111"},
		Daemon:  buildinfo.Info{Version: "0.1.0", Revision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7"},
		Reasons: []string{"managed CLI b062047f does not match daemon 3d5921d9"},
		Action:  activationAction,
	})

	_, err := resolver.FireDueSchedulesWithResult(time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "activation needed") || !strings.Contains(err.Error(), "managed CLI") {
		t.Fatalf("scheduled stale tuple error = %v", err)
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("scheduled stale tuple spawned %d process(es), want 0", got)
	}
	_, err = mgr.Dispatch(DispatchInput{Agent: "worker", Name: "direct-worker", Workspace: filepath.Dir(teamDir)})
	if err == nil || !strings.Contains(err.Error(), "activation needed") {
		t.Fatalf("direct stale tuple error = %v", err)
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("direct stale tuple spawned %d process(es), want 0", got)
	}
}

func TestActivationAllowsCoherentScheduledAndPersistentLaunches(t *testing.T) {
	t.Run("scheduled", func(t *testing.T) {
		teamDir := fixtureTeamDir(t)
		top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "schedule"
match.name = "activation-check"

[schedules.activation-check]
every = "1h"
run_on_start = true

[authority]
enforcement = "enforce"

[authority.instances.worker]
allow = ["job.gate.*:team"]
`)
		fake := newFakeSpawner(100 * time.Millisecond)
		mgr := NewInstanceManager(t.TempDir(), fake.spawn)
		resolver := NewEventResolver(mgr, teamDir, top)
		setActivationForTest(resolver, coherentActivationForTest(t))

		result, err := resolver.FireDueSchedulesWithResult(time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("coherent scheduled launch: %v", err)
		}
		if result.Fired != 1 || fake.callCount() != 1 {
			t.Fatalf("coherent scheduled result=%+v spawn_calls=%d", result, fake.callCount())
		}
	})

	t.Run("persistent", func(t *testing.T) {
		teamDir := fixtureTeamDir(t)
		writeFixtureAgent(t, teamDir, "manager")
		top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
ephemeral = false

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["job.gate.*:team", "job.merge:team"]
`)
		fake := newFakeSpawner(100 * time.Millisecond)
		mgr := NewInstanceManager(t.TempDir(), fake.spawn)
		ctx := activationContextForTest(coherentActivationForTest(t))
		mgr.setActivationContext(ctx)

		meta, launched, err := launchDeclaredFreshWithPrompt(teamDir, mgr, top, top.Find("manager"), nil, "coherent control")
		if err != nil {
			t.Fatalf("coherent persistent launch: %v", err)
		}
		if !launched || meta == nil || fake.callCount() != 1 {
			t.Fatalf("coherent persistent meta=%+v launched=%t spawn_calls=%d", meta, launched, fake.callCount())
		}

		stale := coherentActivationForTest(t)
		stale.State = ActivationStateNeeded
		stale.Reasons = []string{"topology, prompt, or skill assets changed after load"}
		stale.Action = activationAction
		mgr.setActivationContext(activationContextForTest(stale))
		_, _, err = launchDeclaredFreshWithPrompt(teamDir, mgr, top, top.Find("manager"), nil, "stale control")
		if err == nil || !strings.Contains(err.Error(), "activation needed") {
			t.Fatalf("persistent stale tuple error = %v", err)
		}
		if got := fake.callCount(); got != 1 {
			t.Fatalf("persistent stale tuple spawned process; calls=%d", got)
		}
	})

	t.Run("persistent resume regenerates stale bundle", func(t *testing.T) {
		teamDir := fixtureTeamDir(t)
		writeFixtureAgent(t, teamDir, "manager")
		topologyText := `
[instances.manager]
agent = "manager"
ephemeral = false
restart = "on-failure"

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["job.gate.*:team", "job.merge:team"]
`
		if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topologyText), 0o644); err != nil {
			t.Fatal(err)
		}
		top := mustParseCustomTopo(t, topologyText)
		fake := newFakeSpawner(100 * time.Millisecond)
		root := DaemonRoot(teamDir)
		mgr := NewInstanceManager(root, fake.spawn)
		resolver := NewEventResolver(mgr, teamDir, top)
		coherent := coherentActivationForTest(t)
		setActivationForTest(resolver, coherent)
		meta := &Metadata{
			Instance:      "manager",
			Agent:         "manager",
			Runtime:       "codex",
			RuntimeBinary: "codex",
			Workspace:     filepath.Dir(teamDir),
			SessionID:     "old-session",
			Status:        StatusStopped,
			StartedAt:     time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
		}
		if err := WriteMetadata(root, meta); err != nil {
			t.Fatal(err)
		}
		if err := WriteInstanceLaunchEnv(root, "manager", &LaunchEnv{
			Bin:     "codex",
			Args:    []string{"codex", "resume", "old-session"},
			Dir:     filepath.Dir(teamDir),
			Env:     os.Environ(),
			Version: 1,
			Build:   coherent.Daemon,
			Assets:  "stale-assets",
		}); err != nil {
			t.Fatal(err)
		}
		if err := mgr.ensureTracked("manager", meta); err != nil {
			t.Fatal(err)
		}

		status := resolver.activationStatus()
		if status.State != ActivationStateNeeded || len(status.StaleInstances) != 1 || status.StaleInstances[0] != "manager" {
			t.Fatalf("stale persistent status = %+v", status)
		}
		started, err := mgr.Start("manager")
		if err != nil {
			t.Fatalf("fresh fallback from stale resume: %v", err)
		}
		if started == nil || fake.callCount() != 1 {
			t.Fatalf("fresh fallback meta=%+v spawn_calls=%d", started, fake.callCount())
		}
		if args := strings.Join(fake.lastCall(), " "); strings.Contains(args, "old-session") {
			t.Fatalf("stale session was resumed instead of regenerated: %s", args)
		}
		snapshot, err := ReadInstanceLaunchEnv(root, "manager")
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Assets != coherent.LoadedAssets || !buildinfo.SameRevision(snapshot.Build, coherent.Daemon) {
			t.Fatalf("regenerated activation provenance = %+v", snapshot)
		}
	})
}

func TestBuildHandshakeRejectsStaleMutationsButLeavesStatusReadable(t *testing.T) {
	daemonBuild := buildinfo.Info{Version: "0.2.0", Revision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7"}
	clientBuild := buildinfo.Info{Version: "0.1.0", Revision: "b062047f11111111111111111111111111111111"}
	called := 0
	handler := buildHandshakeHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}), daemonBuild, &bytes.Buffer{})

	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodPost, path: "/v1/dispatch", want: http.StatusConflict},
		{method: http.MethodPost, path: "/v1/start", want: http.StatusConflict},
		{method: http.MethodGet, path: "/v1/status", want: http.StatusNoContent},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set(buildinfo.HeaderName, clientBuild.HeaderValue())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if tc.want == http.StatusConflict && !strings.Contains(rec.Body.String(), "activation needed") {
			t.Fatalf("%s %s body=%s", tc.method, tc.path, rec.Body.String())
		}
	}
	if called != 1 {
		t.Fatalf("downstream calls=%d, want only GET status", called)
	}
}

func setActivationForTest(resolver *EventResolver, status ActivationStatus) {
	ctx := activationContextForTest(status)
	resolver.mu.Lock()
	resolver.activation = ctx
	resolver.mu.Unlock()
	resolver.mgr.setActivationContext(ctx)
}

func activationContextForTest(status ActivationStatus) activationContext {
	return activationContext{
		Build:        status.Daemon,
		LoadedAssets: status.LoadedAssets,
		Inspect: func(string, buildinfo.Info, string) ActivationStatus {
			return status
		},
	}
}

func coherentActivationForTest(t *testing.T) ActivationStatus {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	build, err := buildinfo.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if build.Revision == "" {
		build.Revision = "3d5921d9c5d8115359ed1519c9d448981cd5abc7"
	}
	return ActivationStatus{
		State:         ActivationStateCoherent,
		CLIPath:       filepath.Clean(exe),
		CLI:           build,
		Daemon:        build,
		LoadedAssets:  "test-assets",
		CurrentAssets: "test-assets",
	}
}

func TestActivationStatusSummaryExposesBuildAndDriftWithoutShimBypass(t *testing.T) {
	status := ActivationStatus{
		State:             ActivationStateNeeded,
		CLI:               buildinfo.Info{Revision: "b062047f11111111111111111111111111111111"},
		Daemon:            buildinfo.Info{Revision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7"},
		WorkspaceRevision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7",
		Reasons:           []string{"build drift"},
		Action:            activationAction,
	}
	got := status.Summary() + "\n" + status.Diagnostic()
	for _, want := range []string{"activation_needed", "cli=", "daemon=", "workspace=", "activation needed", "restart the daemon"} {
		if !strings.Contains(got, want) {
			t.Fatalf("activation diagnostic missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "go run") || strings.Contains(got, "source checkout") {
		t.Fatalf("activation diagnostic teaches shim bypass:\n%s", got)
	}
}

func TestInstanceBriefRendersActivationTupleAndAction(t *testing.T) {
	brief := &InstanceBrief{
		GeneratedAt: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Instance:    "manager",
		StateDir:    "/repo/.agent_team/state/manager",
		DaemonDir:   "/repo/.agent_team/daemon/instances/manager",
		Activation: &ActivationStatus{
			State:         ActivationStateNeeded,
			CLI:           buildinfo.Info{Revision: "b062047f11111111111111111111111111111111"},
			Daemon:        buildinfo.Info{Revision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7"},
			LoadedAssets:  "11111111111111111111111111111111",
			CurrentAssets: "22222222222222222222222222222222",
			Reasons:       []string{"loaded assets differ from current assets"},
			Action:        activationAction,
		},
	}
	text := RenderInstanceBrief(brief)
	for _, want := range []string{"## Activation", "cli=", "daemon=", "loaded-assets=", "current-assets=", "activation needed", "restart the daemon"} {
		if !strings.Contains(text, want) {
			t.Fatalf("brief missing %q:\n%s", want, text)
		}
	}
}
