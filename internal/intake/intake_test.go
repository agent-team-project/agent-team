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

func TestNormalizeGitHubPRComment(t *testing.T) {
	ev, err := NormalizeGitHub([]byte(`{
  "action": "created",
  "repository": {"full_name": "acme/repo"},
  "issue": {
    "number": 109,
    "title": "Review implementation",
    "pull_request": {
      "html_url": "https://github.com/acme/repo/pull/109",
      "url": "https://api.github.com/repos/acme/repo/pulls/109"
    }
  },
  "comment": {
    "html_url": "https://github.com/acme/repo/pull/109#issuecomment-1"
  }
}`))
	if err != nil {
		t.Fatalf("NormalizeGitHub: %v", err)
	}
	if ev.Type != "pr.commented" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Payload["pr"] != "109" || ev.Payload["pr_url"] != "https://github.com/acme/repo/pull/109" || ev.Payload["issue"] != "109" {
		t.Fatalf("payload = %+v", ev.Payload)
	}
	if ev.Payload["title"] != "Review implementation" || ev.Payload["comment_url"] != "https://github.com/acme/repo/pull/109#issuecomment-1" {
		t.Fatalf("payload = %+v", ev.Payload)
	}
}
