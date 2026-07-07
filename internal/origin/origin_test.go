package origin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureProjectIDBackfillsConfig(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(teamDir, "config.toml")
	if err := os.WriteFile(cfg, []byte("[pm]\nprovider = \"none\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	id, changed, err := EnsureProjectID(teamDir)
	if err != nil {
		t.Fatalf("EnsureProjectID: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if strings.TrimSpace(id) == "" {
		t.Fatal("id is empty")
	}
	got, err := ProjectID(teamDir)
	if err != nil {
		t.Fatalf("ProjectID: %v", err)
	}
	if got != id {
		t.Fatalf("ProjectID = %q, want %q", got, id)
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "[project]\n") {
		t.Fatalf("config missing [project]:\n%s", body)
	}
}

func TestAppendFooter(t *testing.T) {
	body := AppendFooter("hello", Envelope{
		Project:  "project-1",
		Team:     "platform",
		Instance: "worker-squ-90",
		Trigger:  "schedule:feedback-triage",
	})
	for _, want := range []string{
		"hello",
		"agent-team-origin:",
		"project=project-1",
		"team=platform",
		"instance=worker-squ-90",
		"trigger=schedule:feedback-triage",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("footer body missing %q:\n%s", want, body)
		}
	}
	if got := AppendFooter(body, Envelope{Project: "project-2"}); got != body {
		t.Fatalf("AppendFooter duplicated footer:\n%s", got)
	}
}

func TestHeaderValueBackfillsResourceURIs(t *testing.T) {
	value := HeaderValue(Envelope{Project: "dep", Instance: "worker-squ-1", Job: "squ-1"})
	for _, want := range []string{
		"project=dep",
		"deployment_uri=agt://dep/project/dep",
		"instance_uri=agt://dep/instance/worker-squ-1",
		"job_uri=agt://dep/job/squ-1",
	} {
		if !strings.Contains(value, want) {
			t.Fatalf("HeaderValue missing %q in %q", want, value)
		}
	}
	parsed, err := ParseHeaderValue(value)
	if err != nil {
		t.Fatalf("ParseHeaderValue: %v", err)
	}
	if parsed.DeploymentURI != "agt://dep/project/dep" ||
		parsed.InstanceURI != "agt://dep/instance/worker-squ-1" ||
		parsed.JobURI != "agt://dep/job/squ-1" {
		t.Fatalf("parsed URIs = %+v", parsed)
	}
}
