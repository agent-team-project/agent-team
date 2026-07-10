package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/job"
)

func TestSignaturesTestDryRun(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[pipelines.ticket_to_pr.infra_signatures]
fixture_reaped = 'Os \{ code: 2, kind: NotFound'
missing_deps = 'deps/[^ ]*: No such file'

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`), 0o644); err != nil {
		t.Fatalf("write instances.toml: %v", err)
	}
	logPath := filepath.Join(root, "gate.log")
	if err := os.WriteFile(logPath, []byte("failed while opening deps/cache: No such file\nassertion mentioned NotFound but not the infra shape\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"signatures", "test", "ticket_to_pr", "--repo", root, "--against", logPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("signatures test: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{"fixture_reaped", "no-match", "missing_deps", "match", "deps/cache: No such file"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q\nbody:\n%s", want, body)
		}
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"signatures", "test", "ticket_to_pr", "--repo", root, "--against", logPath, "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("signatures test json: %v\nstderr=%s", err, jsonErr.String())
	}
	var result signatureTestResult
	if err := json.Unmarshal(jsonOut.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, jsonOut.String())
	}
	if result.Pipeline != "ticket_to_pr" || len(result.Signatures) != 2 || !result.Signatures[1].Matched || result.Signatures[1].Excerpt != "deps/cache: No such file" {
		t.Fatalf("json result = %+v", result)
	}
}

func TestSignaturesTestBundledGitHubInfraSignatures(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cases := []struct {
		name          string
		pipeline      string
		fixture       string
		wantSignature string
		wantExcerpt   string
	}{
		{
			name:          "ticket project token",
			pipeline:      "ticket_to_pr",
			fixture:       "github_project_token_missing.txt",
			wantSignature: "github_project_token_missing",
			wantExcerpt:   "PROJECTS_TOKEN secret missing",
		},
		{
			name:          "platform project token",
			pipeline:      "platform_ticket_to_pr",
			fixture:       "github_project_token_missing.txt",
			wantSignature: "github_project_token_missing",
			wantExcerpt:   "PROJECTS_TOKEN secret missing",
		},
		{
			name:          "ticket github token",
			pipeline:      "ticket_to_pr",
			fixture:       "github_token_missing.txt",
			wantSignature: "github_token_missing",
			wantExcerpt:   "no GitHub token found",
		},
		{
			name:          "platform github token",
			pipeline:      "platform_ticket_to_pr",
			fixture:       "github_token_missing.txt",
			wantSignature: "github_token_missing",
			wantExcerpt:   "no GitHub token found",
		},
		{
			name:     "ticket assertion failure",
			pipeline: "ticket_to_pr",
			fixture:  "assertion_failure.txt",
		},
		{
			name:     "platform assertion failure",
			pipeline: "platform_ticket_to_pr",
			fixture:  "assertion_failure.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs([]string{
				"signatures", "test", tc.pipeline,
				"--repo", root,
				"--against", filepath.Join("testdata", "signatures", tc.fixture),
				"--json",
			})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("signatures test: %v\nstderr=%s", err, stderr.String())
			}
			var result signatureTestResult
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode result: %v\nbody=%s", err, out.String())
			}
			if result.Pipeline != tc.pipeline {
				t.Fatalf("pipeline = %q, want %q", result.Pipeline, tc.pipeline)
			}
			if tc.wantSignature == "" {
				for _, signature := range result.Signatures {
					if signature.Matched {
						t.Fatalf("signature %q unexpectedly matched assertion fixture: %+v", signature.Name, signature)
					}
				}
				return
			}
			got := signatureResultByName(result.Signatures, tc.wantSignature)
			if got == nil {
				t.Fatalf("signature %q missing from result: %+v", tc.wantSignature, result.Signatures)
			}
			if !got.Matched || !strings.Contains(got.Excerpt, tc.wantExcerpt) {
				t.Fatalf("signature %q = %+v, want match excerpt containing %q", tc.wantSignature, *got, tc.wantExcerpt)
			}
		})
	}
}

func TestBundledGitHubInfraSignaturesTriageClassification(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		id             string
		pipeline       string
		signature      string
		wantClass      string
		wantMatchedSig string
	}{
		{
			id:             "GH272-TICKET-PROJECT",
			pipeline:       "ticket_to_pr",
			signature:      "PROJECTS_TOKEN secret missing",
			wantClass:      jobGateClassInfra,
			wantMatchedSig: "github_project_token_missing",
		},
		{
			id:             "GH272-PLATFORM-PROJECT",
			pipeline:       "platform_ticket_to_pr",
			signature:      "PROJECTS_TOKEN secret missing",
			wantClass:      jobGateClassInfra,
			wantMatchedSig: "github_project_token_missing",
		},
		{
			id:             "GH272-TICKET-GITHUB-TOKEN",
			pipeline:       "ticket_to_pr",
			signature:      "agent-team ticket create: no GitHub token found",
			wantClass:      jobGateClassInfra,
			wantMatchedSig: "github_token_missing",
		},
		{
			id:             "GH272-PLATFORM-GITHUB-TOKEN",
			pipeline:       "platform_ticket_to_pr",
			signature:      "github-auth.sh: no GitHub token found",
			wantClass:      jobGateClassInfra,
			wantMatchedSig: "github_token_missing",
		},
		{
			id:        "GH272-TICKET-CONTENT",
			pipeline:  "ticket_to_pr",
			signature: "TestWidget: assertion failed: got 1, want 2",
			wantClass: jobGateClassContent,
		},
		{
			id:        "GH272-PLATFORM-CONTENT",
			pipeline:  "platform_ticket_to_pr",
			signature: "TestWidget: assertion failed: got 1, want 2",
			wantClass: jobGateClassContent,
		},
	}

	infraIDs := make(map[string]bool)
	contentIDs := make(map[string]bool)
	for i, tc := range cases {
		j := mustNewJob(t, tc.id, "worker")
		j.Pipeline = tc.pipeline
		j.Status = job.StatusRunning
		j.CreatedAt = now.Add(time.Duration(i) * time.Second)
		j.UpdatedAt = j.CreatedAt
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", tc.id, err)
		}

		set := NewRootCmd()
		setOut, setErr := &bytes.Buffer{}, &bytes.Buffer{}
		set.SetOut(setOut)
		set.SetErr(setErr)
		set.SetArgs([]string{
			"job", "gate", "set", tc.id, "smoke",
			"--repo", root,
			"--status", "fail",
			"--signature", tc.signature,
			"--actor", "test",
			"--json",
		})
		if err := set.Execute(); err != nil {
			t.Fatalf("job gate set %s: %v\nstderr=%s", tc.id, err, setErr.String())
		}
		var gate jobGateResult
		if err := json.Unmarshal(setOut.Bytes(), &gate); err != nil {
			t.Fatalf("decode gate result: %v\nbody=%s", err, setOut.String())
		}
		if gate.Class != tc.wantClass || gate.MatchedSignature != tc.wantMatchedSig {
			t.Fatalf("gate %s class/match = %q/%q, want %q/%q", tc.id, gate.Class, gate.MatchedSignature, tc.wantClass, tc.wantMatchedSig)
		}

		normalizedID := job.IDFromInput(tc.id)
		if tc.wantClass == jobGateClassInfra {
			infraIDs[normalizedID] = true
		} else {
			contentIDs[normalizedID] = true
		}
	}

	infraSnapshot := runJobTriageJSON(t, root, "--infra-only")
	assertTriageIDs(t, infraSnapshot.Attention, infraIDs, contentIDs)

	contentSnapshot := runJobTriageJSON(t, root, "--content-only")
	assertTriageIDs(t, contentSnapshot.Attention, contentIDs, infraIDs)
}

func signatureResultByName(results []job.GateSignatureTestResult, name string) *job.GateSignatureTestResult {
	for i := range results {
		if results[i].Name == name {
			return &results[i]
		}
	}
	return nil
}

func assertTriageIDs(t *testing.T, items []jobTriageItem, want, excluded map[string]bool) {
	t.Helper()
	got := make(map[string]bool)
	for _, item := range items {
		got[item.JobID] = true
		if excluded[item.JobID] {
			t.Fatalf("triage unexpectedly included excluded job %s in %+v", item.JobID, items)
		}
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("triage missing job %s in %+v", id, items)
		}
	}
}
