package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/topology"
)

// fixtureTopo parses a small topology used across the event/topology tests.
// One persistent instance (manager) and one ephemeral (worker) with replicas=2.
const fixtureTOML = `
[instances.manager]
agent     = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "user_invocation"

[instances.worker]
agent     = "worker"
ephemeral = true
replicas  = 2

[[instances.worker.triggers]]
event        = "agent.dispatch"
match.target = "worker"
`

func mustParseTopo(t *testing.T) *topology.Topology {
	t.Helper()
	top, err := topology.Parse([]byte(fixtureTOML))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return top
}

func TestEvent_PersistentMessages(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, root, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{"type":"user_invocation","payload":{"name":"manager"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Matched  []string         `json:"matched"`
		Messaged []string         `json:"messaged"`
		Rejected []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Matched) != 1 || got.Matched[0] != "manager" {
		t.Errorf("matched: %v", got.Matched)
	}
	if len(got.Messaged) != 1 || got.Messaged[0] != "manager" {
		t.Errorf("messaged: %v", got.Messaged)
	}
	if len(got.Rejected) != 0 {
		t.Errorf("rejected: %v", got.Rejected)
	}

	// Mailbox file should now contain one message.
	body, err := os.ReadFile(MailboxPath(root, "manager"))
	if err != nil {
		t.Fatalf("mailbox: %v", err)
	}
	if !strings.Contains(string(body), `\"event\":\"user_invocation\"`) {
		t.Errorf("mailbox missing event: %s", string(body))
	}
}

func TestEvent_EphemeralDispatchUnderCapacity(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, filepath.Join(t.TempDir(), ".agent_team"), mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Matched    []string         `json:"matched"`
		Dispatched []map[string]any `json:"dispatched"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Dispatched) != 1 {
		t.Fatalf("expected 1 dispatched, got %+v", got)
	}
	id, _ := got.Dispatched[0]["instance_id"].(string)
	if !strings.HasPrefix(id, "worker-") {
		t.Errorf("instance_id should be unique-prefixed, got %q", id)
	}
	running, queued := resolver.QueueDepth("worker")
	if running != 1 || queued != 0 {
		t.Errorf("counts: running=%d queued=%d", running, queued)
	}
}

func TestEvent_EphemeralReplicasQueueing(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, filepath.Join(t.TempDir(), ".agent_team"), mustParseTopo(t))
	resolver.SetQueueCap(2) // small cap so we can hit it deterministically.
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	post := func(label string) (string, []map[string]any, []string) {
		resp := mustPost(t, srv.URL+"/v1/event",
			`{"type":"agent.dispatch","payload":{"target":"worker"}}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", label, resp.StatusCode, readBody(t, resp))
		}
		var got struct {
			Matched    []string         `json:"matched"`
			Dispatched []map[string]any `json:"dispatched"`
			Queued     []string         `json:"queued"`
			Rejected   []map[string]any `json:"rejected"`
		}
		json.NewDecoder(resp.Body).Decode(&got)
		var rej string
		if len(got.Rejected) > 0 {
			rej, _ = got.Rejected[0]["reason"].(string)
		}
		return rej, got.Dispatched, got.Queued
	}

	// Replicas=2; cap=2; so 4 events fit (2 dispatched + 2 queued); 5th rejected.
	for i := 0; i < 4; i++ {
		_, _, _ = post("post#" + string(rune('A'+i)))
	}
	running, queued := resolver.QueueDepth("worker")
	if running != 2 || queued != 2 {
		t.Errorf("after 4: running=%d queued=%d", running, queued)
	}

	rej, _, _ := post("post#5")
	if rej == "" {
		t.Errorf("5th event should have been rejected, was not")
	}
}

func TestEvent_EmptyPayloadValidation(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, root, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 on missing type, got %d", resp.StatusCode)
	}
}

func TestTopology_GetAndReload(t *testing.T) {
	teamDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(fixtureTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewInstanceManager(t.TempDir(), nil)
	top, _ := topology.LoadFromTeamDir(teamDir)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/topology")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topology get: %d", resp.StatusCode)
	}
	var got struct {
		Instances []map[string]any `json:"instances"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Instances) != 2 {
		t.Errorf("instances: %v", got.Instances)
	}

	// Edit the file → reload.
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.solo]
agent = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	resp = mustPost(t, srv.URL+"/v1/topology/reload", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topology reload: %d %s", resp.StatusCode, readBody(t, resp))
	}
	resp = mustGet(t, srv.URL+"/v1/topology")
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Instances) != 1 || got.Instances[0]["name"] != "solo" {
		t.Errorf("after reload: %v", got.Instances)
	}
}

func TestTopology_NoEventsConfigured(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{"type":"user_invocation"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}

	// /v1/topology returns empty instances list, not 503 — the read path is
	// always-on so clients can render an empty state.
	resp = mustGet(t, srv.URL+"/v1/topology")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on empty topology, got %d", resp.StatusCode)
	}
}
