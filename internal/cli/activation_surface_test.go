package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
)

func TestPsAndDaemonStatusExposeActivationTupleAndAction(t *testing.T) {
	activation := &daemon.ActivationStatus{
		State:         daemon.ActivationStateNeeded,
		CLI:           buildinfo.Info{Revision: "b062047f11111111111111111111111111111111"},
		Daemon:        buildinfo.Info{Revision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7"},
		LoadedAssets:  "11111111111111111111111111111111",
		CurrentAssets: "22222222222222222222222222222222",
		Reasons:       []string{"managed CLI is stale"},
		Action:        "restart with matching binaries",
	}
	status := daemonStatusJSON{Running: true, Reachable: true, Ready: true, Activation: activation}

	var ps bytes.Buffer
	if err := renderPsDaemonReachabilityWarning(&ps, status); err != nil {
		t.Fatal(err)
	}
	var daemonOut bytes.Buffer
	renderDaemonStatus(&daemonOut, status)
	warnings := strings.Join(daemonStatusWarnings(status), "\n")
	got := ps.String() + daemonOut.String() + warnings
	for _, want := range []string{"activation:", "activation_needed", "cli=", "daemon=", "loaded-assets=", "current-assets=", "activation needed", "restart with matching binaries"} {
		if !strings.Contains(got, want) {
			t.Fatalf("activation surfaces missing %q:\n%s", want, got)
		}
	}
}

func TestInstancePsCommandExposesActivationTupleAndAction(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	teamDir, err := filepath.EvalSymlinks(teamDir)
	if err != nil {
		t.Fatal(err)
	}
	activation := &daemon.ActivationStatus{
		State:         daemon.ActivationStateNeeded,
		CLI:           buildinfo.Info{Revision: "b062047f11111111111111111111111111111111"},
		Daemon:        buildinfo.Info{Revision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7"},
		LoadedAssets:  "11111111111111111111111111111111",
		CurrentAssets: "22222222222222222222222222222222",
		Reasons:       []string{"topology, prompt, or skill assets changed after daemon activation"},
		Action:        "restart with matching binaries",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":      true,
			"pid":        os.Getpid(),
			"instances":  1,
			"team_dir":   teamDir,
			"build":      activation.Daemon,
			"activation": activation,
		})
	}))
	defer srv.Close()

	for path, body := range map[string]string{
		daemon.PidPath(teamDir):           strconv.Itoa(os.Getpid()) + "\n",
		daemon.HTTPAddrPath(teamDir):      strings.TrimPrefix(srv.URL, "http://") + "\n",
		daemon.OperatorTokenPath(teamDir): "operator-token\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	socket := daemon.SocketPath(teamDir)
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(socket) })
	if err := os.MkdirAll(filepath.Join(teamDir, "state", "manager"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv("AGENT_TEAM_DAEMON_SOCKET", "")
	t.Setenv(daemon.DaemonTokenFileEnv, "")

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "ps", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance ps: %v\nstderr=%s", err, stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"activation:", "activation_needed", "cli=", "daemon=", "loaded-assets=", "current-assets=",
		"warning: activation needed", "restart with matching binaries", "manager",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instance ps output missing %q:\n%s", want, got)
		}
	}
}
