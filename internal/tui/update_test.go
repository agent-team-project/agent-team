package tui

import (
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

func TestClassifySizeBoundaryAndExhaustiveTotality(t *testing.T) {
	cases := []struct {
		width, height int
		want          SizeClass
	}{
		{59, 50, SizeTooSmall}, {60, 15, SizeTooSmall}, {60, 16, SizeCompact},
		{99, 50, SizeCompact}, {100, 26, SizeCompact}, {100, 27, SizeStandard},
		{100, 40, SizeStandard}, {120, 50, SizeStandard}, {144, 40, SizeStandard},
		{145, 27, SizeStandard}, {145, 30, SizeStandard}, {160, 30, SizeStandard},
		{145, 39, SizeStandard}, {145, 40, SizeWide}, {160, 50, SizeWide},
	}
	for _, test := range cases {
		if got := ClassifySize(test.width, test.height); got != test.want {
			t.Errorf("ClassifySize(%d,%d) = %s, want %s", test.width, test.height, got, test.want)
		}
	}
	valid := map[SizeClass]bool{SizeTooSmall: true, SizeCompact: true, SizeStandard: true, SizeWide: true}
	for width := 0; width <= 320; width++ {
		for height := 0; height <= 120; height++ {
			if class := ClassifySize(width, height); !valid[class] {
				t.Fatalf("invalid class at %dx%d: %q", width, height, class)
			}
		}
	}
}

func TestBootEmitsDiscoveryExactlyOnce(t *testing.T) {
	model := NewModel(fixtureTime, Capabilities{})
	model, commands := Update(model, Boot{})
	if len(commands) != 1 || commands[0].Kind != CommandBootstrap || !model.RefreshInFlight || model.Route != RouteOverview {
		t.Fatalf("first boot model=%+v commands=%+v", model, commands)
	}
	_, commands = Update(model, Boot{})
	if len(commands) != 0 {
		t.Fatalf("second boot commands=%+v", commands)
	}
}

func TestSnapshotTransitionsPreserveLastGoodAndReconnect(t *testing.T) {
	snapshot := smallFixtureSnapshot()
	model := NewModel(fixtureTime, Capabilities{})
	model.Booted = true
	model.RefreshInFlight = true
	for _, source := range daemonclient.SnapshotSources() {
		model, _ = Update(model, SnapshotOK{Source: source, Snapshot: snapshot, At: fixtureTime})
	}
	model, commands := Update(model, RefreshFinished{At: fixtureTime, AnySuccess: true, Complete: true})
	if model.Connection != ConnectionConnected || !model.HasSnapshot() || len(commands) != 1 {
		t.Fatalf("connected model=%+v commands=%+v", model, commands)
	}
	lastJobs := model.Snapshot.Jobs

	model.RefreshInFlight = true
	failedAt := fixtureTime.Add(5 * time.Second)
	model, _ = Update(model, SnapshotError{Source: daemonclient.SourceJobs, Error: "503 unavailable", At: failedAt})
	model, _ = Update(model, RefreshFinished{At: failedAt, Error: "down"})
	if model.Connection != ConnectionDisconnected || len(model.Snapshot.Jobs) != len(lastJobs) {
		t.Fatalf("disconnect lost data: state=%s jobs=%d", model.Connection, len(model.Snapshot.Jobs))
	}
	if !model.Sources[daemonclient.SourceJobs].FetchedAt.Equal(fixtureTime) {
		t.Fatalf("failed refresh changed fetched-at: %+v", model.Sources[daemonclient.SourceJobs])
	}

	model.RefreshInFlight = true
	refreshedAt := fixtureTime.Add(10 * time.Second)
	for _, source := range daemonclient.SnapshotSources() {
		model, _ = Update(model, SnapshotOK{Source: source, Snapshot: snapshot, At: refreshedAt})
	}
	model, _ = Update(model, RefreshFinished{At: refreshedAt, AnySuccess: true, Complete: true})
	if model.Connection != ConnectionReconnected || model.Feedback != "Reconnected" {
		t.Fatalf("reconnect state=%s feedback=%q", model.Connection, model.Feedback)
	}
	model.Polling = false
	model, _ = Update(model, Tick{At: refreshedAt.Add(time.Second)})
	if model.Connection != ConnectionConnected {
		t.Fatalf("ordinary tick after reconnect = %s", model.Connection)
	}
}

func TestCachedStartupIsExplicitlyStale(t *testing.T) {
	model := NewModel(fixtureTime, Capabilities{})
	model, _ = Update(model, CachedSnapshot{Snapshot: smallFixtureSnapshot()})
	if model.Connection != ConnectionStale || !model.HasSnapshot() {
		t.Fatalf("cached model = %+v", model)
	}
	model.RefreshInFlight = true
	model, _ = Update(model, RefreshFinished{At: fixtureTime.Add(time.Second), CacheUsed: true, Error: "daemon: not running"})
	if model.Connection != ConnectionStale || model.Feedback == "" {
		t.Fatalf("cached failure model = %+v", model)
	}
}

func TestResizePreservesSemanticAttentionFocus(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	model.FocusIndex = 2
	model.Focus = focusRing[2]
	model.Focus.ItemID = "release-2026-07"
	for _, size := range [][2]int{{80, 24}, {160, 50}, {120, 30}, {60, 16}} {
		model, _ = Update(model, Resize{Width: size[0], Height: size[1]})
		if model.Focus.ItemID != "release-2026-07" {
			t.Fatalf("focus after %dx%d = %+v", size[0], size[1], model.Focus)
		}
	}
	model, _ = Update(model, QueryChanged{Value: "id:gh383"})
	if model.Focus.ItemID != "gh383-tui-spec" {
		t.Fatalf("filtered fallback focus = %+v", model.Focus)
	}
}

func TestQueryUnknownFieldLeavesResultsUnchanged(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	before := len(projectOverview(model).Attention)
	model, _ = Update(model, QueryChanged{Value: "mystery:value"})
	if model.QueryError == "" || len(projectOverview(model).Attention) != before {
		t.Fatalf("query error=%q results=%d want=%d", model.QueryError, len(projectOverview(model).Attention), before)
	}
}

func TestAdvertisedBindingRegistryDispatches(t *testing.T) {
	keys := []string{"tab", "shift+tab", "up", "down", "left", "right", "h", "j", "k", "l", "enter", "space", "pgup", "pgdown", "home", "end", "[", "]", "r", "p", "/", "esc", "?", "ctrl+k"}
	for _, name := range keys {
		t.Run(name, func(t *testing.T) {
			model := smallFixtureModel(Capabilities{})
			model.RefreshInFlight = false
			updated, commands := Update(model, Key{Name: name, At: fixtureTime})
			if updated.Route == "" || updated.Sources == nil {
				t.Fatal("transition returned an invalid model")
			}
			if name == "r" && (len(commands) != 1 || commands[0].Kind != CommandRefresh) {
				t.Fatalf("refresh commands = %+v", commands)
			}
		})
	}
	model := smallFixtureModel(Capabilities{})
	model, _ = Update(model, Key{Name: "g", At: fixtureTime})
	model, _ = Update(model, Key{Name: "w", At: fixtureTime.Add(500 * time.Millisecond)})
	if model.Route != RouteWork || model.Feedback == "" {
		t.Fatalf("go chord model=%+v", model)
	}
}

func TestQuitClosesModalBeforeProgram(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	model, _ = Update(model, OpenOverlay{Overlay: OverlayHelp})
	model, commands := Update(model, QuitRequested{})
	if model.Quit || len(model.Overlays) != 0 || len(commands) != 0 {
		t.Fatalf("modal quit model=%+v commands=%+v", model, commands)
	}
	model, commands = Update(model, QuitRequested{})
	if !model.Quit || len(commands) != 1 || commands[0].Kind != CommandQuit {
		t.Fatalf("global quit model=%+v commands=%+v", model, commands)
	}
}

func TestAttachTransitionContractPreservesSnapshot(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	snapshot := model.Snapshot
	model, commands := Update(model, AttachRequested{})
	if model.Polling || len(commands) != 1 || commands[0].Kind != CommandAttach {
		t.Fatalf("attach requested model=%+v commands=%+v", model, commands)
	}
	model, _ = Update(model, AttachStarted{})
	model, commands = Update(model, AttachFailed{Error: "child failed"})
	if !model.Polling || model.Snapshot != snapshot || len(commands) != 1 || commands[0].Kind != CommandRefresh {
		t.Fatalf("attach failed model=%+v commands=%+v", model, commands)
	}
	model.Polling = false
	model, commands = Update(model, AttachReturned{})
	if !model.Polling || model.Snapshot != snapshot || len(commands) != 1 || commands[0].Kind != CommandRefresh {
		t.Fatalf("attach returned model=%+v commands=%+v", model, commands)
	}
}
