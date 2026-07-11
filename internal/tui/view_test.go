package tui

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

var sgrPattern = regexp.MustCompile("\\x1b\\[[0-9;]*m")

func TestOverviewProjectionMatchesCanonicalFixture(t *testing.T) {
	projection := projectOverview(smallFixtureModel(Capabilities{}))
	want := OverviewSummary{
		Instances: 6, Running: 4, Jobs: 12, ActiveJobs: 7, BlockedJobs: 2, FailedJobs: 1,
		ModelTiers: 4, BounceClasses: 4, Pipelines: 4, Budgets: 2, Teams: 3, Schedules: 5,
		Deployments: 2, Deadlines: 3,
	}
	if projection.Summary != want {
		t.Fatalf("summary = %+v, want %+v", projection.Summary, want)
	}
	if len(projection.Org) != 7 {
		t.Fatalf("org role rows = %d, want 7", len(projection.Org))
	}
	if len(projection.Attention) != 9 || projection.Attention[0].Status != "failed" {
		t.Fatalf("attention = %+v", projection.Attention)
	}
}

func TestOverviewTelemetryPrecedenceAndRecentWindow(t *testing.T) {
	snapshot := &daemonclient.Snapshot{Resources: map[string]*daemonclient.Resource{}}
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("job-%02d", i)
		jobURI := "agt://dep/job/" + id
		outcomeURI := "agt://dep/outcome/" + id
		snapshot.Jobs = append(snapshot.Jobs, &daemonclient.Job{ID: id, URI: jobURI, OutcomeURI: outcomeURI, UpdatedAt: fixtureTime.Add(-time.Duration(i) * time.Minute)})
		model := "gpt-current"
		if i == 24 {
			model = "gpt-too-old"
		}
		snapshot.Resources[outcomeURI] = testResource(outcomeURI, "outcome", id, map[string]any{"model": model, "tier": "T2"})
	}
	if got := distinctModelTiers(snapshot); got != 1 {
		t.Fatalf("recent-24 model tiers = %d, want 1", got)
	}
	snapshot.Jobs = snapshot.Jobs[:3]
	first := snapshot.Jobs[0]
	snapshot.Resources[first.OutcomeURI] = testResource(first.OutcomeURI, "outcome", first.ID, map[string]any{"bounce_classes": map[string]any{"capability": 1}})
	snapshot.Resources[first.URI] = testResource(first.URI, "job", first.ID, map[string]any{"bounce_classes": map[string]any{"infra": 1}})
	second := snapshot.Jobs[1]
	snapshot.Resources[second.OutcomeURI] = testResource(second.OutcomeURI, "outcome", second.ID, map[string]any{})
	snapshot.Resources[second.URI] = testResource(second.URI, "job", second.ID, map[string]any{"bounce_classes": map[string]any{"scope": 1}})
	third := snapshot.Jobs[2]
	snapshot.Resources[third.OutcomeURI] = testResource(third.OutcomeURI, "outcome", third.ID, map[string]any{})
	snapshot.Resources[third.URI] = testResource(third.URI, "job", third.ID, map[string]any{"kickoff": "## Review findings (bounce 1)\nSpec ambiguity needs clarification."})
	if got := distinctBounceClasses(snapshot); got != 3 {
		t.Fatalf("bounce classes = %d, want capability/scope/spec-ambiguity only", got)
	}
}

func TestCanonicalRendersAreExactStableFrames(t *testing.T) {
	geometries := [][2]int{{80, 24}, {120, 30}, {160, 50}}
	modes := []struct {
		name string
		caps Capabilities
	}{
		{"color", Capabilities{Color: true}},
		{"NO_COLOR", Capabilities{}},
		{"TERM=dumb", Capabilities{Dumb: true}},
	}
	for _, geometry := range geometries {
		for _, mode := range modes {
			t.Run(fmt.Sprintf("%dx%d/%s", geometry[0], geometry[1], mode.name), func(t *testing.T) {
				model := smallFixtureModel(mode.caps)
				model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
				first := Render(model)
				second := Render(model)
				if first != second {
					t.Fatal("two consecutive clean renders differ")
				}
				plain := sgrPattern.ReplaceAllString(first, "")
				assertFrameGeometry(t, plain, geometry[0], geometry[1])
				if mode.name == "color" && !strings.Contains(first, "\x1b[") {
					t.Fatal("color mode emitted no SGR styling")
				}
				if mode.name != "color" && strings.Contains(first, "\x1b") {
					t.Fatalf("plain mode emitted escape byte: %q", first)
				}
			})
		}
	}
}

func TestCanonicalGoldenHashes(t *testing.T) {
	want := map[string]string{
		"80x24/color":      "fdbae9e95edbffc5ed4e6e76589d282d0858a1f2d6c41a74c5b15aaca1a12b98",
		"80x24/NO_COLOR":   "3afb13d009bc43ad8860a41bb7774e9b9a3802ef35eac10fa96c8e13b7403556",
		"80x24/TERM=dumb":  "3afb13d009bc43ad8860a41bb7774e9b9a3802ef35eac10fa96c8e13b7403556",
		"120x30/color":     "706ab7c2999c1490ffc2fc209109f963880656efde1746816fd68847e12a6440",
		"120x30/NO_COLOR":  "8662b8dc7e41990d0f93a108ee638cb09813691ef1c88471616716104c7df230",
		"120x30/TERM=dumb": "8662b8dc7e41990d0f93a108ee638cb09813691ef1c88471616716104c7df230",
		"160x50/color":     "698f6fd8ec0882a3507d4d4cccf1bf721715f595e4b1c14a21221402ea55a2db",
		"160x50/NO_COLOR":  "9674f1e43ae20aeeb9422d127d501f50923ca74111f2905f8e89e68153b5155c",
		"160x50/TERM=dumb": "9674f1e43ae20aeeb9422d127d501f50923ca74111f2905f8e89e68153b5155c",
	}
	modes := []struct {
		name string
		caps Capabilities
	}{{"color", Capabilities{Color: true}}, {"NO_COLOR", Capabilities{}}, {"TERM=dumb", Capabilities{Dumb: true}}}
	for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
		for _, mode := range modes {
			model := smallFixtureModel(mode.caps)
			model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
			frame := Render(model)
			key := fmt.Sprintf("%dx%d/%s", geometry[0], geometry[1], mode.name)
			got := fmt.Sprintf("%x", sha256.Sum256([]byte(frame)))
			if got != want[key] {
				t.Errorf("golden %s hash = %s, want %s", key, got, want[key])
			}
		}
	}
}

func TestCanonicalGoldenFiles(t *testing.T) {
	modes := []struct {
		name string
		caps Capabilities
	}{{"color", Capabilities{Color: true}}, {"no_color", Capabilities{}}, {"term_dumb", Capabilities{Dumb: true}}}
	for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
		for _, mode := range modes {
			model := smallFixtureModel(mode.caps)
			model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
			frame := []byte(Render(model))
			path := filepath.Join("testdata", fmt.Sprintf("overview_%dx%d_%s.golden", geometry[0], geometry[1], mode.name))
			if os.Getenv("UPDATE_TUI_GOLDENS") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, frame, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(frame, want) {
				t.Errorf("golden mismatch: %s (set UPDATE_TUI_GOLDENS=1 to review an intentional update)", path)
			}
		}
	}
}

func TestTermDumbFramesAreASCIIAndControlFree(t *testing.T) {
	for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
		model := smallFixtureModel(Capabilities{Dumb: true})
		model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
		frame := []byte(Render(model))
		for index, value := range frame {
			if value == 0x1b || value == 0x9b || value == 0x9d {
				t.Fatalf("%dx%d forbidden byte %#x at %d", geometry[0], geometry[1], value, index)
			}
			if value >= utf8.RuneSelf {
				t.Fatalf("%dx%d non-ASCII byte %#x at %d", geometry[0], geometry[1], value, index)
			}
		}
		if strings.ContainsAny(string(frame), "┌┐└┘─│├┤┬┴┼") {
			t.Fatalf("%dx%d contains Unicode box drawing", geometry[0], geometry[1])
		}
	}
}

func TestTooSmallFrameIsStableAndUseful(t *testing.T) {
	model := smallFixtureModel(Capabilities{Dumb: true})
	model, _ = Update(model, Resize{Width: 59, Height: 15})
	frame := Render(model)
	assertFrameGeometry(t, frame, 59, 15)
	for _, text := range []string{"TERMINAL TOO SMALL", "59x15", "60x16", "Help", "Quit"} {
		if !strings.Contains(frame, text) {
			t.Errorf("frame missing %q", text)
		}
	}
}

func TestLargeFleetFirstPaint(t *testing.T) {
	model := largeFixtureModel()
	model, _ = Update(model, Resize{Width: 160, Height: 50})
	start := time.Now()
	frame := Render(model)
	elapsed := time.Since(start)
	assertFrameGeometry(t, frame, 160, 50)
	if elapsed > 150*time.Millisecond {
		t.Fatalf("first paint = %s, limit 150ms", elapsed)
	}
	if !strings.Contains(frame, "100 instances") || !strings.Contains(frame, "500 jobs") {
		t.Fatalf("large fixture counts missing from frame")
	}
}

func TestOneHourSoak(t *testing.T) {
	if os.Getenv("AGENT_TEAM_TUI_SOAK") != "1" {
		t.Skip("set AGENT_TEAM_TUI_SOAK=1 for the one-hour acceptance soak")
	}
	model := largeFixtureModel()
	model, _ = Update(model, Resize{Width: 160, Height: 50})
	startGoroutines := runtime.NumGoroutine()
	runtime.GC()
	var startMemory runtime.MemStats
	runtime.ReadMemStats(&startMemory)
	deadline := time.Now().Add(time.Hour)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	iteration := 0
	for now := range ticker.C {
		if now.After(deadline) {
			break
		}
		model.Now = now.UTC()
		if iteration%3 == 0 {
			model, _ = Update(model, Key{Name: "tab", At: model.Now})
		}
		if iteration%7 == 0 {
			widths := [][2]int{{80, 24}, {120, 30}, {160, 50}}
			size := widths[iteration%len(widths)]
			model, _ = Update(model, Resize{Width: size[0], Height: size[1]})
		}
		_ = Render(model)
		iteration++
	}
	runtime.GC()
	var finalMemory runtime.MemStats
	runtime.ReadMemStats(&finalMemory)
	if runtime.NumGoroutine() > startGoroutines+2 {
		t.Fatalf("goroutines grew from %d to %d", startGoroutines, runtime.NumGoroutine())
	}
	limit := startMemory.HeapAlloc + startMemory.HeapAlloc/10 + 1024*1024
	if finalMemory.HeapAlloc > limit {
		t.Fatalf("retained heap grew from %d to %d", startMemory.HeapAlloc, finalMemory.HeapAlloc)
	}
}

func assertFrameGeometry(t *testing.T, frame string, width, height int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) != height {
		t.Fatalf("frame rows = %d, want %d", len(lines), height)
	}
	for row, line := range lines {
		if got := utf8.RuneCountInString(line); got != width {
			t.Fatalf("row %d cells = %d, want %d: %q", row, got, width, line)
		}
	}
}
