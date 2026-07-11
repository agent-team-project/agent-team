package tui

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

var fixtureTime = time.Date(2026, 7, 10, 12, 4, 5, 0, time.UTC)

func smallFixtureModel(capabilities Capabilities) Model {
	snapshot := smallFixtureSnapshot()
	model := NewModel(fixtureTime, capabilities)
	model.Booted = true
	model.Connection = ConnectionConnected
	model.Snapshot = snapshot
	model.LastGoodAt = fixtureTime
	for source, at := range snapshot.SourceTimes {
		model.Sources[source] = SourceState{FetchedAt: at}
	}
	model = preserveFocus(model)
	return model
}

func smallFixtureSnapshot() *daemonclient.Snapshot {
	exit := 1
	instances := []*daemonclient.Instance{
		{Instance: "frontend-worker-1", Agent: "worker", Job: "gh383-tui-spec", Status: daemonclient.InstanceRunning, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/frontend-worker-1", RuntimeDeadline: fixtureTime.Add(time.Hour)},
		{Instance: "platform-worker-2", Agent: "worker", Job: "gh381-research", Status: daemonclient.InstanceRunning, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/platform-worker-2"},
		{Instance: "reviewer-gh382", Agent: "reviewer", Job: "gh382-discord", Status: daemonclient.InstanceRunning, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/reviewer-gh382"},
		{Instance: "manager", Agent: "manager", Status: daemonclient.InstanceRunning, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/manager"},
		{Instance: "verifier-2", Agent: "verifier", Status: daemonclient.InstanceCrashed, ExitCode: &exit, DeploymentURI: "agt://child/project/child", URI: "agt://child/instance/verifier-2"},
		{Instance: "comms", Agent: "comms", Status: daemonclient.InstanceStopped, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/comms"},
	}
	statuses := []daemonclient.JobStatus{
		daemonclient.JobRunning, daemonclient.JobBlocked, daemonclient.JobFailed, daemonclient.JobQueued,
		daemonclient.JobDone, daemonclient.JobRunning, daemonclient.JobDone, daemonclient.JobBlocked,
		daemonclient.JobQueued, daemonclient.JobDone, daemonclient.JobRunning, daemonclient.JobDone,
	}
	jobs := make([]*daemonclient.Job, 12)
	resources := map[string]*daemonclient.Resource{}
	classes := []string{"capability", "scope", "infra", "spec-ambiguity"}
	for i := range jobs {
		id := fmt.Sprintf("job-%02d", i+1)
		if i == 0 {
			id = "gh383-tui-spec"
		}
		if i == 1 {
			id = "release-2026-07"
		}
		uri := "agt://root/job/" + id
		outcomeURI := "agt://root/outcome/" + id
		deployment := "agt://root/project/root"
		if i == 10 {
			deployment = "agt://child/project/child"
		}
		jobs[i] = &daemonclient.Job{
			ID: id, URI: uri, OutcomeURI: outcomeURI, DeploymentURI: deployment,
			Ticket: fmt.Sprintf("GH-%d", 380+i), Target: "worker", Pipeline: "frontend_ticket_to_pr",
			Status: statuses[i], UpdatedAt: fixtureTime.Add(-time.Duration(i) * time.Minute), CreatedAt: fixtureTime.Add(-time.Hour),
		}
		data := map[string]any{}
		if i < 9 {
			data["step_runs"] = []any{map[string]any{"id": "implement", "model": []string{"gpt-5.6", "gpt-5.5", "gpt-5.6"}[i%3], "tier": []string{"T2", "T1", "T3"}[i%3]}}
		}
		if i < 4 {
			data["bounce_classes"] = map[string]any{classes[i]: 1}
		}
		if i == 2 {
			data["deadline"] = fixtureTime.Add(2 * time.Hour).Format(time.RFC3339)
		}
		if i == 7 {
			data["runtime_deadline"] = fixtureTime.Add(3 * time.Hour).Format(time.RFC3339)
		}
		resources[outcomeURI] = testResource(outcomeURI, "outcome", id, data)
		resources[uri] = testResource(uri, "job", id, map[string]any{"status": statuses[i]})
	}
	topology := &daemonclient.Topology{
		Instances: []daemonclient.TopologyInstance{
			{Name: "frontend-worker", Agent: "worker", Replicas: 2, Running: 2, Queued: 1},
			{Name: "platform-worker", Agent: "worker", Replicas: 2, Running: 1},
			{Name: "reviewer", Agent: "reviewer", Replicas: 2, Running: 1},
			{Name: "verifier", Agent: "verifier", Replicas: 2},
			{Name: "manager", Agent: "manager", Replicas: 1, Running: 1},
			{Name: "comms", Agent: "comms", Replicas: 1},
			{Name: "auditor", Agent: "auditor", Replicas: 1},
			{Name: "ticket-manager", Agent: "ticket-manager", Replicas: 1},
		},
		Pipelines: []daemonclient.TopologyPipeline{{Name: "frontend_ticket_to_pr"}, {Name: "platform"}, {Name: "release"}, {Name: "quality"}},
		Teams:     []daemonclient.TopologyTeam{{Name: "frontend"}, {Name: "platform"}, {Name: "quality"}},
		Budgets:   []daemonclient.TopologyBudget{{Team: "frontend"}, {Team: "platform"}},
		Schedules: []daemonclient.TopologySchedule{{Name: "product-verify"}, {Name: "debt-sweep"}, {Name: "docs-freshness"}, {Name: "release"}, {Name: "feedback"}},
	}
	sourceTimes := map[daemonclient.SnapshotSource]time.Time{}
	for _, source := range daemonclient.SnapshotSources() {
		sourceTimes[source] = fixtureTime
	}
	return &daemonclient.Snapshot{
		Schema: daemonclient.SnapshotSchema, TeamDir: "/fixture/.agent_team", DeploymentID: "root", CapturedAt: fixtureTime,
		Instances: instances, Jobs: jobs, Topology: topology, Resources: resources, ResourcesRequested: len(resources),
		SourceTimes: sourceTimes, SourceErrors: map[daemonclient.SnapshotSource]string{},
	}
}

func largeFixtureModel() Model {
	model := smallFixtureModel(Capabilities{})
	snapshot := cloneSnapshot(model.Snapshot)
	snapshot.Instances = make([]*daemonclient.Instance, 100)
	for i := range snapshot.Instances {
		snapshot.Instances[i] = &daemonclient.Instance{Instance: fmt.Sprintf("worker-%03d", i), Agent: "worker", Status: daemonclient.InstanceRunning}
	}
	snapshot.Jobs = make([]*daemonclient.Job, 500)
	for i := range snapshot.Jobs {
		status := daemonclient.JobDone
		if i%5 == 0 {
			status = daemonclient.JobRunning
		}
		snapshot.Jobs[i] = &daemonclient.Job{ID: fmt.Sprintf("job-%03d", i), Ticket: fmt.Sprintf("GH-%d", i), Target: "worker", Status: status, UpdatedAt: fixtureTime.Add(-time.Duration(i) * time.Second)}
	}
	model.Snapshot = snapshot
	model = preserveFocus(model)
	return model
}

func testResource(uri, kind, id string, data map[string]any) *daemonclient.Resource {
	body, _ := json.Marshal(data)
	return &daemonclient.Resource{URI: uri, Kind: kind, ID: id, Data: body}
}
