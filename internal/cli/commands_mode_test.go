package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestValidateCommandsMode(t *testing.T) {
	tests := []struct {
		name string
		opts commandsModeValidation
		want string
	}{
		{
			name: "commands disabled",
			opts: commandsModeValidation{
				Command:  "agent-team test",
				Commands: false,
				Conflicts: []commandsModeConflict{
					commandsModeConflicts("--json", true),
				},
			},
			want: "",
		},
		{
			name: "dry run required",
			opts: commandsModeValidation{
				Command:       "agent-team test",
				Commands:      true,
				RequireDryRun: true,
			},
			want: wantCommandCommandsModeRequiresDryRun("agent-team test") + ".\n",
		},
		{
			name: "first active conflict wins",
			opts: commandsModeValidation{
				Command:  "agent-team test",
				Commands: true,
				Conflicts: []commandsModeConflict{
					commandsModeConflicts("--json", true),
					commandsModeConflicts("--format", true),
				},
			},
			want: wantCommandCommandsModeConflict("agent-team test", "--json") + ".\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			stderr := &bytes.Buffer{}
			cmd.SetErr(stderr)
			err := validateCommandsMode(cmd, tc.opts)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateCommandsMode returned %v", err)
				}
				if stderr.Len() != 0 {
					t.Fatalf("stderr = %q, want empty", stderr.String())
				}
				return
			}
			if err != ExitCode(2) {
				t.Fatalf("validateCommandsMode error = %v, want exit code 2", err)
			}
			if stderr.String() != tc.want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func wantCommandsModeRequiresDryRun() string {
	return "--commands " + "requires --dry-run"
}

func wantCommandsModeConflict(flag string) string {
	return "--commands " + "cannot be combined with " + flag
}

func wantCommandCommandsModeRequiresDryRun(command string) string {
	return command + ": " + wantCommandsModeRequiresDryRun()
}

func wantCommandCommandsModeConflict(command, flag string) string {
	return command + ": " + wantCommandsModeConflict(flag)
}
