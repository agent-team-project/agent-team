package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ApprovalStatus is the durable lifecycle state of a job approval request.
type ApprovalStatus string

const (
	ApprovalStatusPending  ApprovalStatus = "pending"
	ApprovalStatusApproved ApprovalStatus = "approved"
	ApprovalStatusRejected ApprovalStatus = "rejected"
)

// Approval is one durable approval artifact attached to a job.
type Approval struct {
	ID                 string            `json:"id"`
	JobID              string            `json:"job_id"`
	Title              string            `json:"title"`
	Body               string            `json:"body"`
	Status             ApprovalStatus    `json:"status"`
	RequestedAt        time.Time         `json:"requested_at"`
	RequestedBy        string            `json:"requested_by"`
	RequestingInstance string            `json:"requesting_instance,omitempty"`
	StepID             string            `json:"step_id,omitempty"`
	Decision           *ApprovalDecision `json:"decision,omitempty"`
}

// ApprovalDecision records the actor and notes supplied when an approval is
// approved or rejected.
type ApprovalDecision struct {
	TS    time.Time `json:"ts"`
	Actor string    `json:"actor"`
	Notes string    `json:"notes,omitempty"`
}

// ApprovalDirectory returns the directory that contains approval artifacts for
// one job.
func ApprovalDirectory(teamDir, rawJobID string) string {
	return filepath.Join(Directory(teamDir), IDFromInput(rawJobID), "approvals")
}

// ApprovalPath returns the JSON file path for one approval artifact.
func ApprovalPath(teamDir, rawJobID, rawApprovalID string) string {
	return filepath.Join(ApprovalDirectory(teamDir, rawJobID), NormalizeApprovalID(rawApprovalID)+".json")
}

// NormalizeApprovalID turns user-supplied approval ids into file-safe ids.
func NormalizeApprovalID(raw string) string {
	return NormalizeID(raw)
}

// NewApproval builds a pending approval request with normalized ids.
func NewApproval(id, jobID, title, body, requestedBy, requestingInstance, stepID string, now time.Time) (*Approval, error) {
	jobID = IDFromInput(jobID)
	if jobID == "" {
		return nil, errors.New("approval job_id is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("approval title is required")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("approval body is required")
	}
	now = now.UTC()
	id = NormalizeApprovalID(id)
	if id == "" {
		id = newApprovalID(title, now)
	}
	approval := &Approval{
		ID:                 id,
		JobID:              jobID,
		Title:              title,
		Body:               body,
		Status:             ApprovalStatusPending,
		RequestedAt:        now,
		RequestedBy:        strings.TrimSpace(requestedBy),
		RequestingInstance: strings.TrimSpace(requestingInstance),
		StepID:             strings.TrimSpace(stepID),
	}
	if err := ValidateApproval(approval); err != nil {
		return nil, err
	}
	return approval, nil
}

func newApprovalID(title string, now time.Time) string {
	slug := NormalizeApprovalID(title)
	if slug == "" {
		slug = "approval"
	}
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-._")
	}
	stamp := strings.ToLower(now.UTC().Format("20060102t150405.000000000z"))
	return NormalizeApprovalID(stamp + "-" + slug)
}

// ValidApprovalStatus reports whether s is a supported approval status.
func ValidApprovalStatus(s ApprovalStatus) bool {
	switch s {
	case ApprovalStatusPending, ApprovalStatusApproved, ApprovalStatusRejected:
		return true
	default:
		return false
	}
}

// ParseApprovalStatus validates a status string.
func ParseApprovalStatus(raw string) (ApprovalStatus, error) {
	status := ApprovalStatus(strings.ToLower(strings.TrimSpace(raw)))
	if !ValidApprovalStatus(status) {
		return "", fmt.Errorf("unknown approval status %q", raw)
	}
	return status, nil
}

// ValidateApproval checks persisted approval invariants.
func ValidateApproval(approval *Approval) error {
	if approval == nil {
		return errors.New("approval is nil")
	}
	if strings.TrimSpace(approval.ID) == "" {
		return errors.New("approval id is required")
	}
	if normalized := NormalizeApprovalID(approval.ID); normalized != approval.ID {
		return fmt.Errorf("approval id %q must be normalized as %q", approval.ID, normalized)
	}
	if strings.TrimSpace(approval.JobID) == "" {
		return errors.New("approval job_id is required")
	}
	if normalized := IDFromInput(approval.JobID); normalized != approval.JobID {
		return fmt.Errorf("approval job_id %q must be normalized as %q", approval.JobID, normalized)
	}
	if strings.TrimSpace(approval.Title) == "" {
		return errors.New("approval title is required")
	}
	if strings.TrimSpace(approval.Body) == "" {
		return errors.New("approval body is required")
	}
	if !ValidApprovalStatus(approval.Status) {
		return fmt.Errorf("unknown approval status %q", approval.Status)
	}
	if approval.RequestedAt.IsZero() {
		return errors.New("approval requested_at is required")
	}
	if approval.Decision == nil {
		if approval.Status != ApprovalStatusPending {
			return errors.New("approval decision is required for decided approvals")
		}
		return nil
	}
	if approval.Status == ApprovalStatusPending {
		return errors.New("pending approval cannot have a decision")
	}
	if approval.Decision.TS.IsZero() {
		return errors.New("approval decision ts is required")
	}
	if strings.TrimSpace(approval.Decision.Actor) == "" {
		return errors.New("approval decision actor is required")
	}
	return nil
}

// WriteApproval stores an approval artifact atomically.
func WriteApproval(teamDir string, approval *Approval) error {
	if err := ValidateApproval(approval); err != nil {
		return err
	}
	dir := ApprovalDirectory(teamDir, approval.JobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("approval: mkdir: %w", err)
	}
	target := ApprovalPath(teamDir, approval.JobID, approval.ID)
	tmp, err := os.CreateTemp(dir, approval.ID+"-*.json.tmp")
	if err != nil {
		return fmt.Errorf("approval: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(approval); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("approval: encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("approval: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("approval: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("approval: rename: %w", err)
	}
	return nil
}

// ReadApproval loads one approval artifact.
func ReadApproval(teamDir, rawJobID, rawApprovalID string) (*Approval, error) {
	jobID := IDFromInput(rawJobID)
	if jobID == "" {
		return nil, fmt.Errorf("job id %q produced an empty normalized id", rawJobID)
	}
	id := NormalizeApprovalID(rawApprovalID)
	if id == "" {
		return nil, fmt.Errorf("approval id %q produced an empty normalized id", rawApprovalID)
	}
	body, err := os.ReadFile(ApprovalPath(teamDir, jobID, id))
	if err != nil {
		return nil, err
	}
	var approval Approval
	if err := json.Unmarshal(body, &approval); err != nil {
		return nil, fmt.Errorf("approval %s/%s: %w", jobID, id, err)
	}
	if approval.ID == "" {
		approval.ID = id
	}
	if approval.JobID == "" {
		approval.JobID = jobID
	}
	if err := ValidateApproval(&approval); err != nil {
		return nil, fmt.Errorf("approval %s/%s: %w", jobID, id, err)
	}
	return &approval, nil
}

// ListApprovals reads all approval artifacts for one job in deterministic id
// order. A missing approvals directory returns an empty list.
func ListApprovals(teamDir, rawJobID string) ([]*Approval, error) {
	jobID := IDFromInput(rawJobID)
	if jobID == "" {
		return nil, fmt.Errorf("job id %q produced an empty normalized id", rawJobID)
	}
	entries, err := os.ReadDir(ApprovalDirectory(teamDir, jobID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*Approval, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		approval, err := ReadApproval(teamDir, jobID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, approval)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// DecideApproval records an approve/reject decision on a pending approval.
func DecideApproval(approval *Approval, status ApprovalStatus, actor, notes string, now time.Time) error {
	if approval == nil {
		return errors.New("approval is nil")
	}
	if status != ApprovalStatusApproved && status != ApprovalStatusRejected {
		return fmt.Errorf("approval decision status must be %q or %q", ApprovalStatusApproved, ApprovalStatusRejected)
	}
	if approval.Status != ApprovalStatusPending {
		return fmt.Errorf("approval %s is %s, not pending", approval.ID, approval.Status)
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return errors.New("approval decision actor is required")
	}
	approval.Status = status
	approval.Decision = &ApprovalDecision{
		TS:    now.UTC(),
		Actor: actor,
		Notes: strings.TrimSpace(notes),
	}
	return ValidateApproval(approval)
}
