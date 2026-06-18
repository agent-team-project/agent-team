package runtimebin

import "testing"

func TestBinaryDefaultAndEnvOverride(t *testing.T) {
	t.Setenv(EnvBinary, "")
	if got := Binary(); got != DefaultBinary {
		t.Fatalf("default Binary() = %q, want %q", got, DefaultBinary)
	}
	t.Setenv(EnvBinary, "  codex  ")
	if got := Binary(); got != "codex" {
		t.Fatalf("env Binary() = %q, want codex", got)
	}
}
