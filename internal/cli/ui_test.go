package cli

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agent-team-project/agent-team/internal/daemon"
)

func TestUIInteractiveNonTTYRefusesCleanly(t *testing.T) {
	root := writeUITestRepo(t)
	original := uiTerminalOK
	uiTerminalOK = func(_ io.Reader, _ io.Writer) bool { return false }
	t.Cleanup(func() { uiTerminalOK = original })
	var stdout, stderr bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--repo", root, "ui"})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	var exit ExitCode
	if !errors.As(err, &exit) || exit != 2 {
		t.Fatalf("error = %T %v, want exit 2", err, err)
	}
	if stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 || !strings.Contains(stderr.String(), "--once") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestUIOnceNoDaemonRendersHonestFrameAndExitOne(t *testing.T) {
	root := writeUITestRepo(t)
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv("AGENT_TEAM_DAEMON_TOKEN_FILE", "")
	var stdout, stderr bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--repo", root, "ui", "--once"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	var exit ExitCode
	if !errors.As(err, &exit) || exit != 1 {
		t.Fatalf("error = %T %v, want exit 1", err, err)
	}
	assertUIOnceFrame(t, stdout.String())
	if !strings.Contains(stdout.String(), "No daemon snapshot") || !strings.Contains(stdout.String(), "agent-team daemon start") {
		t.Fatalf("frame lacks no-daemon guidance: %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUIOnceUsesAuthenticatedSharedClient(t *testing.T) {
	root := writeUITestRepo(t)
	teamDir := filepath.Join(root, ".agent_team")
	tokenPath := daemon.OperatorTokenPath(teamDir)
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("seed-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer seed-token" {
			t.Errorf("Authorization = %q", got)
		}
		hits++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/instances", "/v1/jobs":
			_, _ = w.Write([]byte("[]\n"))
		case "/v1/topology":
			_, _ = w.Write([]byte("{\"instances\":[],\"pipelines\":[],\"schedules\":[],\"teams\":[],\"budgets\":[]}\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("AGENT_TEAM_DAEMON_URL", server.URL)
	t.Setenv("AGENT_TEAM_DAEMON_TOKEN_FILE", "")
	var stdout, stderr bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--repo", root, "ui", "--once"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v stderr=%s", err, stderr.String())
	}
	assertUIOnceFrame(t, stdout.String())
	if hits != 3 || !strings.Contains(stdout.String(), "CONNECTED") {
		t.Fatalf("hits=%d frame=%s", hits, stdout.String())
	}
}

func writeUITestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[project]\nid = \"ui-test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertUIOnceFrame(t *testing.T, frame string) {
	t.Helper()
	if strings.Contains(frame, "\x1b") {
		t.Fatal("--once emitted a control sequence")
	}
	lines := strings.Split(strings.TrimSuffix(frame, "\n"), "\n")
	if len(lines) != 30 {
		t.Fatalf("frame rows = %d, want 30", len(lines))
	}
	for row, line := range lines {
		if got := utf8.RuneCountInString(line); got != 120 {
			t.Fatalf("row %d cells = %d, want 120", row, got)
		}
	}
}
