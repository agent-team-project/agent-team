package addressing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestViewIncludesSelfParentAndLocalRoutes(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	receiver := filepath.Join(root, "receiver")
	writeConfig(t, filepath.Join(primary, ".agent_team"), `[project]
id = "primary-dep"
parent_uri = "agt://parent-dep/project/parent-dep"

[feedback.routes.receiver]
type = "local"
root = "../receiver"

[feedback.routes.linear]
kind = "linear"
`)
	writeConfig(t, filepath.Join(receiver, ".agent_team"), `[project]
id = "receiver-dep"
`)

	entries, err := View(filepath.Join(primary, ".agent_team"))
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("entries len = %d, want 3: %+v", len(entries), entries)
	}
	assertEntry(t, entries, "self", "agt://primary-dep/project/primary-dep", DeploymentSourceSelf)
	assertEntry(t, entries, "parent", "agt://parent-dep/project/parent-dep", DeploymentSourceParent)
	receiverEntry := assertEntry(t, entries, "receiver", "agt://receiver-dep/project/receiver-dep", DeploymentSourceRoute)
	if receiverEntry.Root != filepath.ToSlash(receiver) {
		t.Fatalf("receiver root = %q, want %q", receiverEntry.Root, filepath.ToSlash(receiver))
	}
}

func TestResolveDeploymentNameAliasAndURI(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	writeConfig(t, teamDir, `[project]
id = "dep"
`)

	for _, name := range []string{"self", "local", ".", "dep"} {
		t.Run(name, func(t *testing.T) {
			entry, err := Resolve(teamDir, name)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", name, err)
			}
			if entry.URI != "agt://dep/project/dep" {
				t.Fatalf("Resolve(%q) URI = %q", name, entry.URI)
			}
		})
	}

	literal, err := Resolve(teamDir, "agt://other/job/squ-127")
	if err != nil {
		t.Fatalf("Resolve literal: %v", err)
	}
	if literal.URI != "agt://other/job/squ-127" || literal.Source != "literal" {
		t.Fatalf("literal = %+v", literal)
	}
}

func TestResolveMissingDeploymentName(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	writeConfig(t, teamDir, `[project]
id = "dep"
`)

	if _, err := Resolve(teamDir, "missing"); err == nil {
		t.Fatal("Resolve missing succeeded")
	}
}

func TestViewRejectsInvalidParentURI(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	writeConfig(t, teamDir, `[project]
id = "dep"
parent_uri = "not-a-uri"
`)

	if _, err := View(teamDir); err == nil {
		t.Fatal("View accepted invalid parent_uri")
	}
}

func writeConfig(t *testing.T, teamDir, body string) {
	t.Helper()
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertEntry(t *testing.T, entries []DeploymentEntry, name, uri, source string) DeploymentEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			if entry.URI != uri || entry.Source != source {
				t.Fatalf("entry %q = %+v, want uri=%q source=%q", name, entry, uri, source)
			}
			return entry
		}
	}
	t.Fatalf("entry %q not found in %+v", name, entries)
	return DeploymentEntry{}
}
