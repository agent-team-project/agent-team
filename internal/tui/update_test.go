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
	for _, binding := range Bindings() {
		binding := binding
		t.Run(binding.ID, func(t *testing.T) {
			model := smallFixtureModel(Capabilities{})
			model.RefreshInFlight = false
			key := binding.Keys[0]
			if binding.ID == "move" || binding.ID == "page" {
				model.FocusIndex = 2
				model = preserveFocus(model)
				rows := projectOverview(model).Attention
				model.Focus.ItemID = rows[min(1, len(rows)-1)].ID
			}
			if binding.ID == "escape" {
				model.Query = "status:blocked"
			}
			if binding.ID == "go" {
				model.Route = RouteWork
				model, _ = Update(model, Key{Name: "g", At: fixtureTime})
				model, _ = Update(model, Key{Name: "o", At: fixtureTime.Add(500 * time.Millisecond)})
				if model.Route != RouteOverview {
					t.Fatalf("go binding did not change route: %+v", model)
				}
				return
			}
			before := model
			updated, commands := Update(model, Key{Name: key, At: fixtureTime})
			switch binding.ID {
			case "quit", "cancel":
				if !updated.Quit || len(commands) != 1 || commands[0].Kind != CommandQuit {
					t.Fatalf("quit transition = %+v commands=%+v", updated, commands)
				}
			case "help":
				if !updated.HasOverlay(OverlayHelp) {
					t.Fatal("help overlay did not open")
				}
			case "palette":
				if !updated.HasOverlay(OverlayPalette) {
					t.Fatal("palette did not open")
				}
			case "query":
				if !updated.QueryActive {
					t.Fatal("query did not activate")
				}
			case "escape":
				if updated.Query != "" {
					t.Fatalf("escape query = %q", updated.Query)
				}
			case "next-focus", "previous-focus":
				if updated.FocusIndex == before.FocusIndex {
					t.Fatal("focus did not move")
				}
			case "move", "page":
				if updated.Focus.ItemID == before.Focus.ItemID {
					t.Fatalf("focused item did not move: %q", updated.Focus.ItemID)
				}
			case "inspect", "toggle", "section":
				if updated.Feedback == before.Feedback || updated.Feedback == "" {
					t.Fatalf("binding feedback did not change: %q", updated.Feedback)
				}
			case "refresh":
				if len(commands) != 1 || commands[0].Kind != CommandRefresh || !updated.RefreshInFlight {
					t.Fatalf("refresh commands = %+v model=%+v", commands, updated)
				}
			case "poll":
				if updated.Polling == before.Polling {
					t.Fatal("polling did not toggle")
				}
			default:
				t.Fatalf("binding %q has no behavior assertion", binding.ID)
			}
		})
	}
}

func TestOverlaysOwnInputSearchSelectAndRestoreInvoker(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	model.FocusIndex = 3
	model = preserveFocus(model)
	invoker := model.Focus
	model, _ = Update(model, Key{Name: "ctrl+k", At: fixtureTime})
	model, _ = Update(model, Key{Name: "q", At: fixtureTime})
	if model.Quit || model.PaletteQuery != "q" || !model.HasOverlay(OverlayPalette) {
		t.Fatalf("palette did not own q: %+v", model)
	}
	model, _ = Update(model, Key{Name: "backspace", At: fixtureTime})
	for _, key := range []string{"w", "o", "r", "k"} {
		model, _ = Update(model, Key{Name: key, At: fixtureTime})
	}
	model, _ = Update(model, Key{Name: "enter", At: fixtureTime})
	if model.HasOverlay(OverlayPalette) || model.Route != RouteWork {
		t.Fatalf("palette selection = %+v", model)
	}

	model.Route = RouteOverview
	model.Focus = invoker
	model.FocusIndex = 3
	model, _ = Update(model, Key{Name: "?", At: fixtureTime})
	model, _ = Update(model, Key{Name: "enter", At: fixtureTime})
	if !model.HasOverlay(OverlayHelp) || model.Feedback != "Help owns input; use PgUp/PgDn, ? or Esc" {
		t.Fatalf("help did not own Enter: %+v", model)
	}
	model, _ = Update(model, Key{Name: "esc", At: fixtureTime})
	if len(model.Overlays) != 0 || model.Focus != invoker || model.FocusIndex != 3 {
		t.Fatalf("overlay invoker was not restored: %+v", model)
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
