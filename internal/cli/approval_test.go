package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
)

func TestApprovalRequestApproveLinksManualGateAndNotifiesRequester(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:         "squ-47",
		Ticket:     "SQU-47",
		Target:     "worker",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusBlocked,
		LastEvent:  "step_blocked",
		LastStatus: "waiting for plan approval",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{{
			ID:               "review",
			Target:           "manager",
			Status:           job.StatusBlocked,
			Gate:             job.StepGateManual,
			ApprovalRequired: true,
		}},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	bodyPath := filepath.Join(root, "plan.md")
	if err := os.WriteFile(bodyPath, []byte("Approve this implementation plan."), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}
	t.Setenv("AGENT_TEAM_INSTANCE", "manager")

	request := NewRootCmd()
	requestOut, requestErr := &bytes.Buffer{}, &bytes.Buffer{}
	request.SetOut(requestOut)
	request.SetErr(requestErr)
	request.SetArgs([]string{
		"approval", "request",
		"--repo", root,
		"--job", "squ-47",
		"--id", "plan",
		"--title", "Plan approval",
		"--body-file", bodyPath,
		"--step", "review",
		"--json",
	})
	if err := request.Execute(); err != nil {
		t.Fatalf("approval request: %v\nstderr=%s", err, requestErr.String())
	}
	var requested job.Approval
	if err := json.Unmarshal(requestOut.Bytes(), &requested); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, requestOut.String())
	}
	if requested.ID != "plan" || requested.Status != job.ApprovalStatusPending || requested.RequestingInstance != "manager" || requested.StepID != "review" {
		t.Fatalf("requested approval = %+v", requested)
	}
	linked, err := job.Read(teamDir, "squ-47")
	if err != nil {
		t.Fatalf("read linked job: %v", err)
	}
	if linked.Steps[0].ApprovalID != "plan" || linked.Steps[0].ApprovalStatus != job.ApprovalStatusPending {
		t.Fatalf("linked step = %+v", linked.Steps[0])
	}

	bypass := NewRootCmd()
	bypassErr := &bytes.Buffer{}
	bypass.SetErr(bypassErr)
	bypass.SetArgs([]string{"job", "approve", "squ-47", "--repo", root, "--step", "review"})
	if err := bypass.Execute(); err == nil {
		t.Fatalf("job approve bypass succeeded unexpectedly")
	}
	if !strings.Contains(bypassErr.String(), "requires approval") {
		t.Fatalf("job approve bypass stderr = %q", bypassErr.String())
	}

	approve := NewRootCmd()
	approveOut, approveErr := &bytes.Buffer{}, &bytes.Buffer{}
	approve.SetOut(approveOut)
	approve.SetErr(approveErr)
	approve.SetArgs([]string{
		"approval", "approve", "plan",
		"--repo", root,
		"--job", "squ-47",
		"--actor", "supervisor",
		"--notes", "plan is acceptable",
		"--json",
	})
	if err := approve.Execute(); err != nil {
		t.Fatalf("approval approve: %v\nstderr=%s", err, approveErr.String())
	}
	var approved job.Approval
	if err := json.Unmarshal(approveOut.Bytes(), &approved); err != nil {
		t.Fatalf("decode approve: %v\nbody=%s", err, approveOut.String())
	}
	if approved.Status != job.ApprovalStatusApproved || approved.Decision == nil || approved.Decision.Actor != "supervisor" || approved.Decision.Notes != "plan is acceptable" {
		t.Fatalf("approved = %+v", approved)
	}
	updated, err := job.Read(teamDir, "squ-47")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusQueued || updated.Steps[0].Status != job.StatusQueued || updated.Steps[0].ApprovalStatus != job.ApprovalStatusApproved {
		t.Fatalf("updated job = %+v step=%+v", updated, updated.Steps[0])
	}
	events, err := job.ListEvents(teamDir, "squ-47")
	if err != nil {
		t.Fatalf("job events: %v", err)
	}
	if len(events) != 2 || events[0].Type != "approval.requested" || events[1].Type != "approval.decided" {
		t.Fatalf("events = %+v", events)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read mailbox: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"approval_id":"plan"`) || !strings.Contains(messages[0].Body, `"status":"approved"`) {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestApprovalApproveRejectsMissingCurrentAttemptEvidence(t *testing.T) {
	root, teamDir, j, approval, _ := setupApprovalApproveEvidenceTest(t, nil)

	err, stderr := runApprovalApproveEvidenceTest(t, root, j.ID, approval.ID)
	if err == nil || !strings.Contains(stderr, `step "verify" lacks passing gate evidence`) {
		t.Fatalf("approval without evidence: err=%v stderr=%q", err, stderr)
	}
	assertApprovalDecisionUnchanged(t, teamDir, j.ID, approval.ID)
}

func TestApprovalApproveRejectsFailedCurrentAttemptEvidence(t *testing.T) {
	root, teamDir, j, approval, head := setupApprovalApproveEvidenceTest(t, []job.GateRecord{
		{Step: "verify", Name: "go-test", Status: job.GateStatusFail},
		{Step: "review", Name: "review", Status: job.GateStatusPass},
	})

	err, stderr := runApprovalApproveEvidenceTest(t, root, j.ID, approval.ID)
	if err == nil || !strings.Contains(stderr, `step "verify" has failing gate "go-test"`) {
		t.Fatalf("approval with failed evidence at head %s: err=%v stderr=%q", head, err, stderr)
	}
	assertApprovalDecisionUnchanged(t, teamDir, j.ID, approval.ID)
}

func TestApprovalApproveAcceptsPassingCurrentAttemptEvidence(t *testing.T) {
	root, teamDir, j, approval, head := setupApprovalApproveEvidenceTest(t, []job.GateRecord{
		{Step: "verify", Name: "go-test", Status: job.GateStatusPass},
		{Step: "review", Name: "review", Status: job.GateStatusPass},
	})

	err, stderr := runApprovalApproveEvidenceTest(t, root, j.ID, approval.ID)
	if err != nil {
		t.Fatalf("approval with passing evidence at head %s: err=%v stderr=%q", head, err, stderr)
	}
	persistedApproval, err := job.ReadApproval(teamDir, j.ID, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedApproval.Status != job.ApprovalStatusApproved || persistedApproval.Decision == nil {
		t.Fatalf("persisted approval = %+v", persistedApproval)
	}
	persistedJob, err := job.Read(teamDir, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	step := persistedJob.Steps[3]
	if step.Status != job.StatusQueued || step.ApprovalStatus != job.ApprovalStatusApproved {
		t.Fatalf("persisted approval step = %+v", step)
	}
}

func setupApprovalApproveEvidenceTest(t *testing.T, records []job.GateRecord) (string, string, *job.Job, *job.Approval, string) {
	t.Helper()
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Now().UTC()
	head := strings.Repeat("b", 40)
	j := &job.Job{
		ID: "gh-230-approval-route", Ticket: "GH-230", Target: "worker", Pipeline: "ticket_to_pr",
		Attempt: 2, Head: head, Status: job.StatusBlocked, CreatedAt: now, UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusDone},
			{ID: "verify", Target: "verifier", Status: job.StatusDone, After: []string{"implement"}},
			{ID: "review", Target: "reviewer", Status: job.StatusDone, After: []string{"verify"}},
			{
				ID: "approve", Target: "manager", Status: job.StatusBlocked, After: []string{"review"},
				Gate: job.StepGateManual, ApprovalRequired: true, ApprovalID: "release", ApprovalStatus: job.ApprovalStatusPending,
			},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatal(err)
	}
	approval, err := job.NewApproval("release", j.ID, "Release approval", "Approve the current implementation.", "manager", "manager", "approve", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := job.WriteApproval(teamDir, approval); err != nil {
		t.Fatal(err)
	}
	for i := range records {
		records[i].JobID = j.ID
		records[i].Attempt = j.Attempt
		records[i].Commit = head
		if err := job.AppendGateRecord(teamDir, &records[i]); err != nil {
			t.Fatal(err)
		}
	}
	return root, teamDir, j, approval, head
}

func runApprovalApproveEvidenceTest(t *testing.T, root, jobID, approvalID string) (error, string) {
	t.Helper()
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"approval", "approve", approvalID, "--repo", root, "--job", jobID})
	return cmd.Execute(), stderr.String()
}

func assertApprovalDecisionUnchanged(t *testing.T, teamDir, jobID, approvalID string) {
	t.Helper()
	persistedApproval, err := job.ReadApproval(teamDir, jobID, approvalID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedApproval.Status != job.ApprovalStatusPending || persistedApproval.Decision != nil {
		t.Fatalf("approval mutated after rejected decision = %+v", persistedApproval)
	}
	persistedJob, err := job.Read(teamDir, jobID)
	if err != nil {
		t.Fatal(err)
	}
	step := persistedJob.Steps[3]
	if step.Status != job.StatusBlocked || step.ApprovalStatus != job.ApprovalStatusPending {
		t.Fatalf("job mutated after rejected decision: step=%+v", step)
	}
}
