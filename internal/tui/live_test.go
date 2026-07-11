package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

func TestSeededLiveDaemonOverviewMatchesTypedAPIValues(t *testing.T) {
	fixture := smallFixtureSnapshot()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/instances", func(w http.ResponseWriter, _ *http.Request) { encodeLiveJSON(t, w, fixture.Instances) })
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, _ *http.Request) { encodeLiveJSON(t, w, fixture.Jobs) })
	mux.HandleFunc("/v1/topology", func(w http.ResponseWriter, _ *http.Request) { encodeLiveJSON(t, w, fixture.Topology) })
	mux.HandleFunc("/v1/resources", func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.Query().Get("uri")
		resource := fixture.Resources[uri]
		if resource == nil {
			resource = testResource(uri, "fixture", uri, map[string]any{})
		}
		encodeLiveJSON(t, w, resource)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := daemonclient.NewHTTP(server.URL, "", daemonclient.Options{RoundTripper: server.Client().Transport})

	live := client.Snapshot(context.Background(), fixtureTime)
	if !live.Complete() {
		t.Fatalf("live snapshot errors = %v", live.SourceErrors)
	}
	model := NewModel(fixtureTime, Capabilities{})
	model.Booted = true
	for _, source := range daemonclient.SnapshotSources() {
		model, _ = Update(model, SnapshotOK{Source: source, Snapshot: live, At: fixtureTime})
	}
	model.RefreshInFlight = true
	model, _ = Update(model, RefreshFinished{At: fixtureTime, AnySuccess: true, Complete: true})
	projection := projectOverview(model)
	if projection.Summary.Instances != len(fixture.Instances) || projection.Summary.Jobs != len(fixture.Jobs) {
		t.Fatalf("collection parity = %+v", projection.Summary)
	}
	if projection.Summary.Pipelines != len(fixture.Topology.Pipelines) || projection.Summary.Budgets != len(fixture.Topology.Budgets) || projection.Summary.Teams != len(fixture.Topology.Teams) || projection.Summary.Schedules != len(fixture.Topology.Schedules) {
		t.Fatalf("topology parity = %+v", projection.Summary)
	}
	if projection.Summary.ModelTiers != 4 || projection.Summary.BounceClasses != 4 {
		t.Fatalf("resource parity = %+v", projection.Summary)
	}
}

func encodeLiveJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
