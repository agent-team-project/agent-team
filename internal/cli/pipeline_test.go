package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
)

func TestPipelineListAndShow(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ls := NewRootCmd()
	lsOut, lsErr := &bytes.Buffer{}, &bytes.Buffer{}
	ls.SetOut(lsOut)
	ls.SetErr(lsErr)
	ls.SetArgs([]string{"pipeline", "ls", "--repo", root})
	if err := ls.Execute(); err != nil {
		t.Fatalf("pipeline ls: %v\nstderr=%s", err, lsErr.String())
	}
	for _, want := range []string{"PIPELINE", "ticket_to_pr", "ticket.created", "implement:worker", "review:manager after=implement"} {
		if !strings.Contains(lsOut.String(), want) {
			t.Fatalf("pipeline ls missing %q:\n%s", want, lsOut.String())
		}
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"pipeline", "show", "ticket_to_pr", "--repo", root})
	if err := show.Execute(); err != nil {
		t.Fatalf("pipeline show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"Pipeline: ticket_to_pr", "Trigger:  ticket.created", "implement target=worker after=-", "review target=manager after=implement"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("pipeline show missing %q:\n%s", want, showOut.String())
		}
	}

	asJSON := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	asJSON.SetOut(jsonOut)
	asJSON.SetErr(jsonErr)
	asJSON.SetArgs([]string{"pipeline", "ls", "--repo", root, "--json"})
	if err := asJSON.Execute(); err != nil {
		t.Fatalf("pipeline ls json: %v\nstderr=%s", err, jsonErr.String())
	}
	var rows []pipelineInfo
	if err := json.Unmarshal(jsonOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline json: %v\nbody=%s", err, jsonOut.String())
	}
	if len(rows) != 1 || rows[0].Name != "ticket_to_pr" || len(rows[0].Steps) != 2 {
		t.Fatalf("pipeline rows = %+v", rows)
	}
}

func TestPipelineShowMissing(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "show", "missing", "--repo", root})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline show missing succeeded")
	}
	if !strings.Contains(stderr.String(), `pipeline "missing" not found`) {
		t.Fatalf("missing stderr = %q", stderr.String())
	}
}

func TestPipelineJobsListsMatchingJobs(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{ID: "squ-301", Ticket: "SQU-301", Target: "worker", Pipeline: "ticket_to_pr", Status: job.StatusRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-302", Ticket: "SQU-302", Target: "manager", Pipeline: "nightly", Status: job.StatusQueued, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-303", Ticket: "SQU-303", Target: "manager", Pipeline: "ticket_to_pr", Status: job.StatusDone, CreatedAt: now, UpdatedAt: now},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--status", "running", "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs json: %v\nstderr=%s", err, jsonErr.String())
	}
	var rows []job.Job
	if err := json.Unmarshal(jsonOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline jobs json: %v\nbody=%s", err, jsonOut.String())
	}
	if len(rows) != 1 || rows[0].ID != "squ-301" {
		t.Fatalf("pipeline job rows = %+v", rows)
	}

	formatCmd := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatCmd.SetOut(formatOut)
	formatCmd.SetErr(formatErr)
	formatCmd.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--format", "{{.ID}} {{.Status}}"})
	if err := formatCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.Split(strings.TrimSpace(formatOut.String()), "\n"); strings.Join(got, ",") != "squ-301 running,squ-303 done" {
		t.Fatalf("pipeline jobs format output = %q", formatOut.String())
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--status", "waiting"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("pipeline jobs invalid status succeeded")
	}
	if !strings.Contains(invalidErr.String(), "unknown job status") {
		t.Fatalf("invalid status stderr = %q", invalidErr.String())
	}
}
