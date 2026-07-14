package cli

import (
	"bytes"
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
