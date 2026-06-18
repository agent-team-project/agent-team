package intake

import "testing"

func TestNormalizeLinearIssueCreated(t *testing.T) {
	ev, err := NormalizeLinear([]byte(`{
  "action": "Issue created",
  "data": {
    "id": "issue-id",
    "identifier": "SQU-100",
    "title": "Add intake",
    "url": "https://linear.app/squirtlesquad/issue/SQU-100/add-intake",
    "team": {"key": "SQU"},
    "project": {"name": "Agent Team"},
    "state": {"name": "Todo"}
  }
}`))
	if err != nil {
		t.Fatalf("NormalizeLinear: %v", err)
	}
	if ev.Type != "ticket.created" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Payload["ticket"] != "SQU-100" || ev.Payload["team"] != "SQU" || ev.Payload["status"] != "Todo" {
		t.Fatalf("payload = %+v", ev.Payload)
	}
}

func TestNormalizeGitHubPRMerged(t *testing.T) {
	ev, err := NormalizeGitHub([]byte(`{
  "action": "closed",
  "repository": {"full_name": "jamesaud/agent-team"},
  "pull_request": {
    "number": 42,
    "title": "Add queue",
    "html_url": "https://github.com/jamesaud/agent-team/pull/42",
    "merged": true,
    "head": {"ref": "worktree-worker-squ-42"},
    "base": {"ref": "main"}
  }
}`))
	if err != nil {
		t.Fatalf("NormalizeGitHub: %v", err)
	}
	if ev.Type != "pr.merged" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Payload["pr"] != "42" || ev.Payload["repository"] != "jamesaud/agent-team" || ev.Payload["merged"] != true {
		t.Fatalf("payload = %+v", ev.Payload)
	}
}
