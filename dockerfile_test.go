package agentteam

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDockerfilePinsCodexNPMVersion(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	versionRe := regexp.MustCompile(`(?m)^ARG CODEX_NPM_VERSION=([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)$`)
	matches := versionRe.FindStringSubmatch(content)
	if matches == nil {
		t.Fatal("Dockerfile must declare CODEX_NPM_VERSION as a concrete semver pin")
	}

	const install = `npm install -g "@openai/codex@${CODEX_NPM_VERSION}"`
	if !strings.Contains(content, install) {
		t.Fatalf("Dockerfile must install Codex through the pinned CODEX_NPM_VERSION arg: missing %q", install)
	}

	for _, floating := range []string{
		"npm install -g @openai/codex",
		"@openai/codex@latest",
	} {
		if strings.Contains(content, floating) {
			t.Fatalf("Dockerfile must not use a floating Codex npm install: found %q", floating)
		}
	}

	for _, note := range []string{
		"Keep Codex pinned",
		"npm latest",
		"tarball",
	} {
		if !strings.Contains(content, note) {
			t.Fatalf("Dockerfile must keep the Codex pin rationale in a nearby note: missing %q", note)
		}
	}
}
