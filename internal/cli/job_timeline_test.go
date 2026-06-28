package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestJobTimelineCombinesAuditAndLifecycle(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-170",
		Ticket:    "SQU-170",
		Target:    "worker",
		Status:    job.StatusRunning,
		Instance:  "worker-squ-170",
		CreatedAt: now,
		UpdatedAt: now.Add(2 * time.Minute),
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, ev := range []job.Event{
		{TS: now, JobID: j.ID, Type: "created", Status: job.StatusQueued, Actor: "cli", Message: "created job"},
		{TS: now.Add(2 * time.Minute), JobID: j.ID, Type: "note", Status: job.StatusRunning, Actor: "operator", Message: "checked progress"},
	} {
		if err := job.AppendEvent(teamDir, &ev); err != nil {
			t.Fatalf("append job event: %v", err)
		}
	}
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "life-170",
		TS:       now.Add(time.Minute),
		Action:   "dispatch",
		Instance: j.Instance,
		Agent:    "worker",
		Job:      j.ID,
		Status:   daemon.StatusRunning,
		Message:  "started worker",
	}); err != nil {
		t.Fatalf("append lifecycle event: %v", err)
	}
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "life-other",
		TS:       now.Add(3 * time.Minute),
		Action:   "dispatch",
		Instance: "other-worker",
		Job:      "squ-999",
		Message:  "unrelated",
	}); err != nil {
		t.Fatalf("append unrelated lifecycle event: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--tail", "2", "--sort", "newest", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job timeline: %v\nstderr=%s", err, stderr.String())
	}
	var entries []jobTimelineEntry
	if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
		t.Fatalf("decode timeline: %v\nbody=%s", err, out.String())
	}
	if len(entries) != 2 {
		t.Fatalf("timeline entries = %+v", entries)
	}
	if entries[0].Source != "job" || entries[0].Kind != "note" || entries[1].Source != "lifecycle" || entries[1].Kind != "dispatch" {
		t.Fatalf("timeline order = %+v", entries)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Message, "unrelated") {
			t.Fatalf("timeline included unrelated event: %+v", entries)
		}
	}

	formatted := NewRootCmd()
	formattedOut, formattedErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formattedOut)
	formatted.SetErr(formattedErr)
	formatted.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--source", "lifecycle", "--format", "{{.Source}}:{{.Kind}}:{{.Instance}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("job timeline format: %v\nstderr=%s", err, formattedErr.String())
	}
	if got, want := formattedOut.String(), "lifecycle:dispatch:worker-squ-170\n"; got != want {
		t.Fatalf("timeline format = %q, want %q", got, want)
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--source", "bogus"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("job timeline invalid source succeeded")
	}
	if !strings.Contains(invalidErr.String(), "--source must be all, job, or lifecycle") {
		t.Fatalf("invalid source stderr = %q", invalidErr.String())
	}
}
