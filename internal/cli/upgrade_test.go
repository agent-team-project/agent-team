package cli

import (
	"bytes"
	"encoding/json"
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

func TestUpgradeCheckJSONAndStrict(t *testing.T) {
	tmplDir := t.TempDir()
	writeTinyTemplate(t, tmplDir, "tiny", "0.0.1", "hello")

	target := t.TempDir()
	initCmd := NewRootCmd()
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetErr(&bytes.Buffer{})
	initCmd.SetArgs([]string{"init", tmplDir, "--target", target})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init local template: %v", err)
	}

	nextDir := t.TempDir()
	writeTinyTemplate(t, nextDir, "tiny", "0.0.2", "hello again")

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"upgrade", "--check", "--json", "--strict", "--target", target, "--to", nextDir})
	err := cmd.Execute()
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("strict drift err = %v, want exit 1\nstderr=%s", err, errOut.String())
	}
	var result upgradeCheckResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode upgrade json: %v\nbody=%s", err, out.String())
	}
	if !result.Differs || result.UpToDate || result.TargetTemplate != "tiny" || result.TargetVersion != "0.0.2" || result.ApplyImplemented {
		t.Fatalf("upgrade json result = %+v", result)
	}
	if result.LockedHash == "" || result.TargetHash == "" || result.LockedHash == result.TargetHash {
		t.Fatalf("upgrade hashes = %+v", result)
	}
	if errOut.Len() != 0 {
		t.Fatalf("strict json stderr = %q", errOut.String())
	}
}

func TestUpgradeCheckFormatAndStrict(t *testing.T) {
	tmplDir := t.TempDir()
	writeTinyTemplate(t, tmplDir, "tiny", "0.0.1", "hello")

	target := t.TempDir()
	initCmd := NewRootCmd()
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetErr(&bytes.Buffer{})
	initCmd.SetArgs([]string{"init", tmplDir, "--target", target})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init local template: %v", err)
	}

	nextDir := t.TempDir()
	writeTinyTemplate(t, nextDir, "tiny", "0.0.2", "hello again")

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"upgrade", "--check", "--strict",
		"--format", "{{.Differs}} {{.TargetVersion}} {{.ApplyImplemented}}",
		"--target", target,
		"--to", nextDir,
	})
	err := cmd.Execute()
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("strict format err = %v, want exit 1\nstderr=%s", err, errOut.String())
	}
	if got, want := out.String(), "true 0.0.2 false\n"; got != want {
		t.Fatalf("upgrade --format output = %q, want %q", got, want)
	}
	if errOut.Len() != 0 {
		t.Fatalf("strict format stderr = %q", errOut.String())
	}
}

func TestUpgradeCheckFormatValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{
			args: []string{"upgrade", "--check", "--format", "{{.Differs}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			args: []string{"upgrade", "--check", "--format", "{{"},
			want: "invalid --format template",
		},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(errOut)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		var ec ExitCode
		if !errors.As(err, &ec) || int(ec) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", tc.args, err)
		}
		if !strings.Contains(errOut.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, errOut.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: validation should not write stdout: %q", tc.args, out.String())
		}
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
