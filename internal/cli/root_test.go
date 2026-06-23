package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jamesaud/agent-team/internal/job"
)

func TestRootRepoFlagSelectsRepoForRepoScopedCommands(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--repo", root, "job", "ls", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ls with root --repo: %v\nstderr=%s", err, stderr.String())
	}
	var jobs []job.Job
	if err := json.Unmarshal(out.Bytes(), &jobs); err != nil {
		t.Fatalf("decode jobs: %v\nbody=%s", err, out.String())
	}
	if len(jobs) != 0 {
		t.Fatalf("jobs = %+v, want none", jobs)
	}
}

func TestRootRepoFlagDoesNotConflictWithJobTargetAgent(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--repo", root, "job", "create", "SQU-707", "--target", "manager", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job create with root --repo and agent --target: %v\nstderr=%s", err, stderr.String())
	}
	var created job.Job
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("decode created job: %v\nbody=%s", err, out.String())
	}
	if created.ID != "squ-707" || created.Target != "manager" {
		t.Fatalf("created = %+v, want manager-targeted squ-707", created)
	}
}

func TestRootRepoFlagWorksAfterLegacyTargetCommand(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan with --repo: %v\nstderr=%s", err, stderr.String())
	}
	var plan planResult
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v\nbody=%s", err, out.String())
	}
	if len(plan.Instances) == 0 {
		t.Fatalf("plan = %+v, want declared instances", plan)
	}
}
