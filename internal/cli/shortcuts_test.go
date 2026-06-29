package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestShortcutsListsTopLevelAliases(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"shortcuts"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shortcuts: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{
		"ALIAS",
		"COMMAND",
		"agent-team up",
		"agent-team start",
		"agent-team down",
		"agent-team stop",
		"agent-team ls",
		"agent-team ps",
		"agent-team top",
		"agent-team stats",
		"agent-team exec",
		"agent-team attach",
		"agent-team teams",
		"agent-team team",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shortcuts output missing %q\nbody:\n%s", want, body)
		}
	}
	if strings.Contains(body, "agent-team team start") {
		t.Fatalf("default shortcuts included nested alias:\n%s", body)
	}
	if stderr.Len() != 0 {
		t.Fatalf("shortcuts wrote stderr: %s", stderr.String())
	}
}

func TestShortcutsJSON(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"shortcuts", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shortcuts --json: %v\nstderr=%s", err, stderr.String())
	}
	var shortcuts []shortcutInfo
	if err := json.Unmarshal(out.Bytes(), &shortcuts); err != nil {
		t.Fatalf("decode shortcuts json: %v\nbody=%s", err, out.String())
	}
	if !hasShortcut(shortcuts, "agent-team up", "agent-team start") {
		t.Fatalf("shortcuts json missing up alias: %+v", shortcuts)
	}
	if hasShortcut(shortcuts, "agent-team team start", "agent-team team up") {
		t.Fatalf("shortcuts json unexpectedly included nested team alias: %+v", shortcuts)
	}
	if stderr.Len() != 0 {
		t.Fatalf("shortcuts --json wrote stderr: %s", stderr.String())
	}
}

func TestShortcutsAllIncludesNestedAliases(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"shortcuts", "--all", "--format", "{{.Alias}}={{.Command}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shortcuts --all --format: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{
		"agent-team team start=agent-team team up",
		"agent-team team stop=agent-team team down",
		"agent-team job exec=agent-team job attach",
		"agent-team pipeline top=agent-team pipeline stats",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shortcuts --all output missing %q\nbody:\n%s", want, body)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("shortcuts --all --format wrote stderr: %s", stderr.String())
	}
}

func TestShortcutsRejectsInvalidOutputFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json and format",
			args: []string{"shortcuts", "--json", "--format", "{{.Alias}}"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "invalid format",
			args: []string{"shortcuts", "--format", "{{"},
			want: "invalid --format template",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("shortcuts %s unexpectedly succeeded\nstdout=%s", tc.name, out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr missing %q\nstderr=%s", tc.want, stderr.String())
			}
			if out.Len() != 0 {
				t.Fatalf("validation wrote stdout: %s", out.String())
			}
		})
	}
}

func hasShortcut(shortcuts []shortcutInfo, alias, command string) bool {
	for _, shortcut := range shortcuts {
		if shortcut.Alias == alias && shortcut.Command == command {
			return true
		}
	}
	return false
}
