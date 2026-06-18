package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineListAndShow(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ls := NewRootCmd()
	lsOut, lsErr := &bytes.Buffer{}, &bytes.Buffer{}
	ls.SetOut(lsOut)
	ls.SetErr(lsErr)
	ls.SetArgs([]string{"pipeline", "ls", "--repo", root})
	if err := ls.Execute(); err != nil {
		t.Fatalf("pipeline ls: %v\nstderr=%s", err, lsErr.String())
	}
	for _, want := range []string{"PIPELINE", "ticket_to_pr", "ticket.created", "implement:worker", "review:manager after=implement"} {
		if !strings.Contains(lsOut.String(), want) {
			t.Fatalf("pipeline ls missing %q:\n%s", want, lsOut.String())
		}
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"pipeline", "show", "ticket_to_pr", "--repo", root})
	if err := show.Execute(); err != nil {
		t.Fatalf("pipeline show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"Pipeline: ticket_to_pr", "Trigger:  ticket.created", "implement target=worker after=-", "review target=manager after=implement"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("pipeline show missing %q:\n%s", want, showOut.String())
		}
	}

	asJSON := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	asJSON.SetOut(jsonOut)
	asJSON.SetErr(jsonErr)
	asJSON.SetArgs([]string{"pipeline", "ls", "--repo", root, "--json"})
	if err := asJSON.Execute(); err != nil {
		t.Fatalf("pipeline ls json: %v\nstderr=%s", err, jsonErr.String())
	}
	var rows []pipelineInfo
	if err := json.Unmarshal(jsonOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline json: %v\nbody=%s", err, jsonOut.String())
	}
	if len(rows) != 1 || rows[0].Name != "ticket_to_pr" || len(rows[0].Steps) != 2 {
		t.Fatalf("pipeline rows = %+v", rows)
	}
}

func TestPipelineShowMissing(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "show", "missing", "--repo", root})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline show missing succeeded")
	}
	if !strings.Contains(stderr.String(), `pipeline "missing" not found`) {
		t.Fatalf("missing stderr = %q", stderr.String())
	}
}
