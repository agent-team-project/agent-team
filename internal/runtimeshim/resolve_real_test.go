package runtimeshim

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveRealAgentTeamPrefersSiblingNotDaemon is the SQU-152 regression: when
// the DAEMON installs a shim, os.Executable() is agent-teamd; the shim must resolve
// the sibling agent-team CLI, never agent-teamd (whose name shares the prefix).
func TestResolveRealAgentTeamPrefersSiblingNotDaemon(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "agent-team")
	daemon := filepath.Join(dir, "agent-teamd")
	for _, p := range []string{cli, daemon} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Current executable is the daemon; a stray agent-team on PATH would be wrong to pick.
	lookPath := func(string) (string, error) { return "/should/not/be/used", nil }
	got, err := resolveRealAgentTeamFrom("", daemon, lookPath)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != cli {
		t.Fatalf("resolved %q, want sibling CLI %q (must not be the daemon)", got, cli)
	}
}

func TestResolveRealAgentTeamExactCLI(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "agent-team")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveRealAgentTeamFrom("", cli, nil)
	if err != nil || got != cli {
		t.Fatalf("resolve exact CLI = (%q,%v), want (%q,nil)", got, err, cli)
	}
}
