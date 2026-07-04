package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type commandsModeValidation struct {
	Command       string
	Commands      bool
	RequireDryRun bool
	DryRun        bool
	Conflicts     []commandsModeConflict
}

type commandsModeConflict struct {
	Flag   string
	Active bool
}

func commandsModeConflicts(flag string, active bool) commandsModeConflict {
	return commandsModeConflict{Flag: flag, Active: active}
}

func validateCommandsMode(cmd *cobra.Command, opts commandsModeValidation) error {
	if !opts.Commands {
		return nil
	}
	command := opts.Command
	if command == "" {
		command = cmd.CommandPath()
	}
	if opts.RequireDryRun && !opts.DryRun {
		return commandsModeUsageError(cmd, command, "--commands requires --dry-run")
	}
	for _, conflict := range opts.Conflicts {
		if conflict.Active {
			return commandsModeUsageError(cmd, command, "--commands cannot be combined with "+conflict.Flag)
		}
	}
	return nil
}

func commandsModeUsageError(cmd *cobra.Command, command, message string) error {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s.\n", command, message)
	return exitErr(2)
}
