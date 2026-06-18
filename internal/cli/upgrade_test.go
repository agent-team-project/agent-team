package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeCheck_BundledUpToDate(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"upgrade", "--check", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade --check: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	for _, want := range []string{
		"Locked ref: bundled",
		"Target ref: bundled",
		"Locked hash: sha256:",
		"Target hash: sha256:",
		"already up to date",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("upgrade output missing %q\nfull:\n%s", want, body)
		}
	}
}

func TestUpgradeCheck_DetectsDifferentTarget(t *testing.T) {
	tmplDir := t.TempDir()
	writeTinyTemplate(t, tmplDir, "tiny", "0.0.1", "hello")

	target := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"init", tmplDir, "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init local template: %v", err)
	}

	nextDir := t.TempDir()
	writeTinyTemplate(t, nextDir, "tiny", "0.0.2", "hello again")

	cmd2 := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd2.SetOut(out)
	cmd2.SetErr(errOut)
	cmd2.SetArgs([]string{"upgrade", "--check", "--target", target, "--to", nextDir})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("upgrade --check --to: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	if !strings.Contains(body, "Target template: tiny v0.0.2") {
		t.Errorf("missing target version in output: %s", body)
	}
	if !strings.Contains(body, "template differs") {
		t.Errorf("missing differs result: %s", body)
	}
}

func TestUpgradeRequiresCheckForNow(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"upgrade", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected upgrade without --check to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "pass --check") {
		t.Errorf("missing --check guidance: %s", errOut.String())
	}
}

func TestUpgradeCheck_FailsWithoutLock(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	if err := os.Remove(filepath.Join(tmp, ".agent_team", ".template.lock")); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"upgrade", "--check", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing lock to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), ".template.lock") {
		t.Errorf("missing lock path in error: %s", errOut.String())
	}
}

func writeTinyTemplate(t *testing.T, dir, name, version, body string) {
	t.Helper()
	manifest := `[template]
name = "` + name + `"
version = "` + version + `"
`
	if err := os.WriteFile(filepath.Join(dir, "template.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "skills", "tiny"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "tiny", "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
