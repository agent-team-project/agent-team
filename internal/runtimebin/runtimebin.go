package runtimebin

import (
	"os"
	"strings"
)

const (
	DefaultBinary = "claude"
	EnvBinary     = "AGENT_TEAM_RUNTIME_BIN"
)

func Binary() string {
	if value := strings.TrimSpace(os.Getenv(EnvBinary)); value != "" {
		return value
	}
	return DefaultBinary
}
