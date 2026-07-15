package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	scrubAgentTeamEnvForTestProcess()
	cleanup := installManagedCLITestBinary()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func installManagedCLITestBinary() func() {
	dir, err := os.MkdirTemp("", "daemon-test-agent-team")
	if err != nil {
		panic(err)
	}
	out := filepath.Join(dir, "agent-team")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if body, err := exec.Command(goBinary, "build", "-o", out, "github.com/agent-team-project/agent-team/cmd/agent-team").CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		panic(string(body))
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		_ = os.RemoveAll(dir)
		panic(err)
	}
	return func() {
		_ = os.Setenv("PATH", oldPath)
		_ = os.RemoveAll(dir)
	}
}

// Tests should behave like CI even when launched from an agent-team worker.
func scrubAgentTeamEnvForTestProcess() {
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "AGENT_TEAM_") {
			_ = os.Unsetenv(key)
		}
	}
}
