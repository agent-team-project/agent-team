package topology

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	templatecfg "github.com/agent-team-project/agent-team/internal/template"
)

const frontendDeclaredEvidenceContract = "Every metric the issue or SPEC declares for an evidence artifact must actually be measured; if a metric cannot be measured on this host, fail the gate loudly with the reason — a sentinel value (-1, null, or 'skipped') recorded in evidence is a failing gate, never a pass."

func TestFrontendProgramDeclaredEvidenceContract(t *testing.T) {
	for _, fixture := range frontendProgramTopologies(t) {
		t.Run(fixture.name, func(t *testing.T) {
			if missing := missingFrontendDeclaredEvidenceContract(fixture.top); len(missing) != 0 {
				t.Fatalf("frontend declared-evidence contract missing from steps: %s", strings.Join(missing, ", "))
			}

			for _, stepID := range []string{"implement", "verify"} {
				step := frontendProgramStep(fixture.top, stepID)
				if step == nil {
					t.Fatalf("frontend %s step is missing", stepID)
				}
				normalized := normalizeFrontendInstructions(step.Instructions)

				t.Run("delete-"+stepID, func(t *testing.T) {
					original := step.Instructions
					t.Cleanup(func() { step.Instructions = original })
					step.Instructions = strings.Replace(normalized, frontendDeclaredEvidenceContract, "", 1)
					if step.Instructions == normalized {
						t.Fatal("declared-evidence deletion mutation did not change instructions")
					}
					if missing := missingFrontendDeclaredEvidenceContract(fixture.top); len(missing) != 1 || missing[0] != stepID {
						t.Fatalf("deleting %s contract reports missing steps %v, want [%s]", stepID, missing, stepID)
					}
				})

				t.Run("counterfeit-pass-"+stepID, func(t *testing.T) {
					original := step.Instructions
					t.Cleanup(func() { step.Instructions = original })
					step.Instructions = strings.Replace(normalized, "never a pass.", "may still pass.", 1)
					if step.Instructions == normalized {
						t.Fatal("fail-open counterfeit mutation did not change instructions")
					}
					if missing := missingFrontendDeclaredEvidenceContract(fixture.top); len(missing) != 1 || missing[0] != stepID {
						t.Fatalf("counterfeiting %s contract reports missing steps %v, want [%s]", stepID, missing, stepID)
					}
				})
			}
		})
	}
}

func TestFrontendProgramSoakBudgets(t *testing.T) {
	for _, fixture := range frontendProgramTopologies(t) {
		t.Run(fixture.name, func(t *testing.T) {
			pipeline := fixture.top.Pipelines["frontend_ticket_to_pr"]
			if pipeline == nil {
				t.Fatal("frontend_ticket_to_pr pipeline is missing")
			}
			for _, want := range []struct {
				id         string
				timeout    time.Duration
				timeBudget time.Duration
			}{
				{id: "implement", timeout: 3 * time.Hour, timeBudget: 3 * time.Hour},
				{id: "verify", timeout: 2 * time.Hour, timeBudget: 2 * time.Hour},
			} {
				var step *PipelineStep
				for _, candidate := range pipeline.Steps {
					if candidate.ID == want.id {
						step = candidate
						break
					}
				}
				if step == nil {
					t.Fatalf("frontend %s step is missing", want.id)
				}
				if step.Timeout != want.timeout || step.TimeBudget != want.timeBudget {
					t.Fatalf("frontend %s timeout/time budget = %s/%s, want %s/%s", want.id, step.Timeout, step.TimeBudget, want.timeout, want.timeBudget)
				}
			}
		})
	}
}

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

func missingFrontendDeclaredEvidenceContract(top *Topology) []string {
	missing := make([]string, 0, 2)
	for _, stepID := range []string{"implement", "verify"} {
		step := frontendProgramStep(top, stepID)
		if step == nil || !strings.Contains(normalizeFrontendInstructions(step.Instructions), frontendDeclaredEvidenceContract) {
			missing = append(missing, stepID)
		}
	}
	return missing
}

func frontendProgramStep(top *Topology, stepID string) *PipelineStep {
	if top == nil || top.Pipelines["frontend_ticket_to_pr"] == nil {
		return nil
	}
	for _, step := range top.Pipelines["frontend_ticket_to_pr"].Steps {
		if step.ID == stepID {
			return step
		}
	}
	return nil
}

func normalizeFrontendInstructions(instructions string) string {
	return strings.Join(strings.Fields(instructions), " ")
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
