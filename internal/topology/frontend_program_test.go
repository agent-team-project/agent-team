package topology

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	templatecfg "github.com/agent-team-project/agent-team/internal/template"
)

func TestFrontendProgramAuthorityContract(t *testing.T) {
	for _, fixture := range frontendProgramTopologies(t) {
		t.Run(fixture.name, func(t *testing.T) {
			top := fixture.top
			owner := top.Instances["frontend-manager"]
			if owner == nil || owner.Ephemeral || owner.Agent != "manager" {
				t.Fatalf("frontend owner = %+v, want persistent manager", owner)
			}
			team := top.Teams["frontend"]
			if team == nil || !containsTopologyRef(team.Instances, owner.Name) || !containsTopologyRef(team.Pipelines, "frontend_ticket_to_pr") {
				t.Fatalf("frontend team = %+v", team)
			}
			pipeline := top.Pipelines["frontend_ticket_to_pr"]
			if pipeline == nil || pipeline.ReapWorktree != "on_merge" {
				t.Fatalf("frontend pipeline = %+v", pipeline)
			}
			if !frontendManagerReceivesGateReady(owner) {
				t.Fatal("frontend-manager lacks exact job.step_completed target trigger")
			}
			for _, verb := range []string{"job.bounce", "job.step", "job.gate.set", "job.approve", "job.reject", "job.merge"} {
				decision := AuthorityDecision{
					Instance:   owner.Name,
					Agent:      owner.Agent,
					Team:       top.TeamForInstance(owner.Name),
					Verb:       verb,
					TargetTeam: "frontend",
				}
				if eval := top.Authority.Evaluate(decision); !eval.Allowed {
					t.Fatalf("frontend manager denied %s: %+v", verb, eval)
				}
			}
		})
	}
}

func TestFrontendProgramDeadOwnGrantMutationIsRejected(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".agent_team", "instances.toml"))
	if err != nil {
		t.Fatalf("read self-dogfood topology: %v", err)
	}
	mutated := strings.Replace(string(body),
		`[authority.instances.frontend-manager]
allow = ["event.publish", "job.events", "job.gate.*:team", "job.step:team", "job.bounce:team", "job.approve:team", "job.reject:team", "job.close:team", "job.merge:team", "read", "ticket.create", "ticket.comment", "ticket.update"]`,
		`[authority.instances.frontend-manager]
allow = ["event.publish", "job.events", "job.gate.*:own", "job.step:own", "job.bounce:own", "job.approve:own", "job.reject:own", "job.close:own", "job.merge:own", "read", "ticket.create", "ticket.comment", "ticket.update"]`,
		1,
	)
	if mutated == string(body) {
		t.Fatal("authority mutation did not change fixture")
	}
	_, err = Parse([]byte(mutated))
	assertAuthoritySatisfiabilityError(t, err, `lacks effective authority "job.bounce:team"`)
}

type frontendProgramFixture struct {
	name string
	top  *Topology
}

func frontendProgramTopologies(t *testing.T) []frontendProgramFixture {
	t.Helper()
	selfBody, err := os.ReadFile(filepath.Join("..", "..", ".agent_team", "instances.toml"))
	if err != nil {
		t.Fatalf("read self-dogfood topology: %v", err)
	}
	self, err := Parse(selfBody)
	if err != nil {
		t.Fatalf("parse self-dogfood topology: %v", err)
	}

	templateBody, err := os.ReadFile(filepath.Join("..", "..", "template", "instances.toml.tmpl"))
	if err != nil {
		t.Fatalf("read topology template: %v", err)
	}
	data := templatecfg.Tree{}
	data.SetDotted("template.profile", "full")
	data.SetDotted("pm.provider", "github")
	data.SetDotted("github.owner", "acme")
	data.SetDotted("github.repo", "frontend")
	data.SetDotted("github.agent_column", "Ready for Agent")
	renderedBody, err := templatecfg.RenderBytes("instances.toml.tmpl", templateBody, data)
	if err != nil {
		t.Fatalf("render full topology template: %v", err)
	}
	rendered, err := Parse(renderedBody)
	if err != nil {
		t.Fatalf("parse full topology template: %v", err)
	}

	return []frontendProgramFixture{
		{name: "self-dogfood", top: self},
		{name: "full template", top: rendered},
	}
}

func frontendManagerReceivesGateReady(owner *Instance) bool {
	if owner == nil {
		return false
	}
	for _, trigger := range owner.Triggers {
		if trigger == nil || trigger.Event != EventJobStepCompleted {
			continue
		}
		if target := trigger.Match["target"]; target.Single == owner.Name {
			return true
		}
	}
	return false
}

func containsTopologyRef(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
