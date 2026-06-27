package cli

import (
	"fmt"
	"io"
	"strings"
)

func renderActionCommands(w io.Writer, actions []string) error {
	seen := map[string]bool{}
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if action == "" || seen[action] {
			continue
		}
		seen[action] = true
		if _, err := fmt.Fprintln(w, action); err != nil {
			return err
		}
	}
	return nil
}
