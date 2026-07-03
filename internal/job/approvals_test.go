package job

import (
	"path/filepath"
	"testing"
	"time"
)

func TestApprovalReadWriteListDecide(t *testing.T) {
	teamDir := t.TempDir()
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	approval, err := NewApproval("plan-review", "SQU-47", "Plan review", "Please review the plan.", "manager", "manager", "review", now)
	if err != nil {
		t.Fatalf("NewApproval: %v", err)
	}
	if err := WriteApproval(teamDir, approval); err != nil {
		t.Fatalf("WriteApproval: %v", err)
	}
	if got := ApprovalPath(teamDir, "SQU-47", "plan-review"); got != filepath.Join(teamDir, "jobs", "squ-47", "approvals", "plan-review.json") {
		t.Fatalf("ApprovalPath = %q", got)
	}

	read, err := ReadApproval(teamDir, "squ-47", "plan-review")
	if err != nil {
		t.Fatalf("ReadApproval: %v", err)
	}
	if read.ID != "plan-review" || read.JobID != "squ-47" || read.Status != ApprovalStatusPending || read.StepID != "review" {
		t.Fatalf("read approval = %+v", read)
	}
	list, err := ListApprovals(teamDir, "SQU-47")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(list) != 1 || list[0].ID != "plan-review" {
		t.Fatalf("list = %+v", list)
	}

	decidedAt := now.Add(time.Hour)
	if err := DecideApproval(read, ApprovalStatusApproved, "supervisor", "looks good", decidedAt); err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	if err := WriteApproval(teamDir, read); err != nil {
		t.Fatalf("WriteApproval decided: %v", err)
	}
	decided, err := ReadApproval(teamDir, "squ-47", "plan-review")
	if err != nil {
		t.Fatalf("ReadApproval decided: %v", err)
	}
	if decided.Status != ApprovalStatusApproved || decided.Decision == nil || decided.Decision.Actor != "supervisor" || decided.Decision.Notes != "looks good" {
		t.Fatalf("decided approval = %+v", decided)
	}
}

func TestApprovalRequiredStepReadiness(t *testing.T) {
	now := time.Now().UTC()
	j := &Job{
		ID:        "squ-47",
		Ticket:    "SQU-47",
		Target:    "worker",
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []Step{{
			ID:               "review",
			Target:           "manager",
			Status:           StatusQueued,
			Gate:             StepGateManual,
			ApprovalRequired: true,
			ApprovalID:       "plan-review",
			ApprovalStatus:   ApprovalStatusPending,
		}},
	}
	if step := NextReadyStep(j); step != nil {
		t.Fatalf("NextReadyStep pending approval = %+v, want nil", step)
	}
	j.Steps[0].ApprovalStatus = ApprovalStatusApproved
	if step := NextReadyStep(j); step == nil || step.ID != "review" {
		t.Fatalf("NextReadyStep approved = %+v, want review", step)
	}
}

func TestValidateApprovalRequiredStepRequiresManualGate(t *testing.T) {
	j := mustValidJobForTest(t)
	j.Steps = []Step{{
		ID:               "review",
		Target:           "manager",
		Status:           StatusBlocked,
		Gate:             StepGatePR,
		ApprovalRequired: true,
	}}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted approval_required on non-manual gate")
	}
}

func mustValidJobForTest(t *testing.T) *Job {
	t.Helper()
	now := time.Now().UTC()
	return &Job{
		ID:        "squ-47",
		Ticket:    "SQU-47",
		Target:    "worker",
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
