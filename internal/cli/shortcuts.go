package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/spf13/cobra"
)

type shortcutInfo struct {
	Alias       string `json:"alias"`
	Command     string `json:"command"`
	Description string `json:"description"`
}

func newShortcutsCmd() *cobra.Command {
	var (
		all     bool
		jsonOut bool
		format  string
	)
	cmd := &cobra.Command{
		Use:   "shortcuts",
		Short: "List command aliases and Docker-like shortcuts.",
		Long: "List command aliases and Docker-like shortcuts from the live command tree. " +
			"By default this shows top-level shortcuts; use --all to include nested command-group aliases.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team shortcuts: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseShortcutFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team shortcuts: %v\n", err)
				return exitErr(2)
			}
			shortcuts := collectShortcutInfos(cmd.Root(), all)
			return renderShortcutList(cmd.OutOrStdout(), shortcuts, jsonOut, tmpl)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include nested aliases under command groups.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit shortcuts as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each shortcut with a Go template, e.g. '{{.Alias}} -> {{.Command}}'.")
	return cmd
}

func collectShortcutInfos(root *cobra.Command, includeNested bool) []shortcutInfo {
	if root == nil {
		return nil
	}
	var shortcuts []shortcutInfo
	var walk func(*cobra.Command, int)
	walk = func(cmd *cobra.Command, depth int) {
		if cmd == nil || cmd.Hidden {
			return
		}
		if includeNested || depth == 1 {
			for _, alias := range cmd.Aliases {
				alias = strings.TrimSpace(alias)
				if alias == "" {
					continue
				}
				shortcuts = append(shortcuts, shortcutInfo{
					Alias:       shortcutAliasPath(cmd, alias),
					Command:     cmd.CommandPath(),
					Description: strings.TrimSpace(cmd.Short),
				})
			}
		}
		if !includeNested && depth >= 1 {
			return
		}
		children := append([]*cobra.Command(nil), cmd.Commands()...)
		sort.SliceStable(children, func(i, j int) bool {
			return children[i].CommandPath() < children[j].CommandPath()
		})
		for _, child := range children {
			walk(child, depth+1)
		}
	}

	children := append([]*cobra.Command(nil), root.Commands()...)
	sort.SliceStable(children, func(i, j int) bool {
		return children[i].CommandPath() < children[j].CommandPath()
	})
	for _, child := range children {
		walk(child, 1)
	}
	sort.SliceStable(shortcuts, func(i, j int) bool {
		if shortcuts[i].Alias == shortcuts[j].Alias {
			return shortcuts[i].Command < shortcuts[j].Command
		}
		return shortcuts[i].Alias < shortcuts[j].Alias
	})
	return shortcuts
}

func shortcutAliasPath(cmd *cobra.Command, alias string) string {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) == 0 {
		return alias
	}
	parts[len(parts)-1] = alias
	return strings.Join(parts, " ")
}

func renderShortcutList(w io.Writer, shortcuts []shortcutInfo, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(shortcuts)
	}
	if tmpl != nil {
		for _, shortcut := range shortcuts {
			if err := renderShortcutFormat(w, shortcut, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(shortcuts) == 0 {
		fmt.Fprintln(w, "(no shortcuts)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tCOMMAND\tDESCRIPTION")
	for _, shortcut := range shortcuts {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", shortcut.Alias, shortcut.Command, shortcut.Description)
	}
	_ = tw.Flush()
	return nil
}

func parseShortcutFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("shortcut-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderShortcutFormat(w io.Writer, shortcut shortcutInfo, tmpl *template.Template) error {
	if err := tmpl.Execute(w, shortcut); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
