package daemon

import (
	"strings"
	"testing"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestAuditAuthorityTeamScopeUsesTargetJobOrigin(t *testing.T) {
	top, err := topology.Parse([]byte(`
[instances.frontend-manager]
agent = "manager"

[teams.frontend]
instances = ["frontend-manager"]

[authority]
enforcement = "enforce"

[authority.instances.frontend-manager]
allow = ["job.bounce:team"]
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	teamDir := t.TempDir()
	writeAuthorityTargetJob(t, teamDir, "GH-403", "frontend")
	writeAuthorityTargetJob(t, teamDir, "GH-398", "research")
	actor := origin.Envelope{Instance: "frontend-manager", Agent: "manager", Team: "frontend"}

	if err := AuditAuthority(AuthorityAuditOptions{
		TeamDir:   teamDir,
		Topology:  top,
		Actor:     actor,
		Verb:      "job.bounce",
		TargetJob: "gh-403",
		Resource:  "job:gh-403",
	}); err != nil {
		t.Fatalf("team-owned job denied: %v", err)
	}

	err = AuditAuthority(AuthorityAuditOptions{
		TeamDir:   teamDir,
		Topology:  top,
		Actor:     actor,
		Verb:      "job.bounce",
		TargetJob: "gh-398",
		Resource:  "job:gh-398",
	})
	if err == nil || !strings.Contains(err.Error(), "authority violation") {
		t.Fatalf("cross-team job error = %v, want authority violation", err)
	}
}

func TestAuditAuthorityPersistentManagerOwnScopeRemainsUnsatisfied(t *testing.T) {
	top, err := topology.Parse([]byte(`
[instances.frontend-manager]
agent = "manager"

[teams.frontend]
instances = ["frontend-manager"]

[authority]
enforcement = "enforce"

[authority.instances.frontend-manager]
allow = ["job.bounce:own"]
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	teamDir := t.TempDir()
	writeAuthorityTargetJob(t, teamDir, "GH-403", "frontend")
	err = AuditAuthority(AuthorityAuditOptions{
		TeamDir:   teamDir,
		Topology:  top,
		Actor:     origin.Envelope{Instance: "frontend-manager", Agent: "manager", Team: "frontend"},
		Verb:      "job.bounce",
		TargetJob: "gh-403",
		Resource:  "job:gh-403",
	})
	if err == nil || !strings.Contains(err.Error(), "authority violation") {
		t.Fatalf("persistent manager :own error = %v, want authority violation", err)
	}
}

func writeAuthorityTargetJob(t *testing.T, teamDir, ticket, team string) {
	t.Helper()
	j, err := jobstore.New(ticket, "worker", "authority scope test", time.Now().UTC())
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Origin = origin.Envelope{Team: team, Job: j.ID}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
}
