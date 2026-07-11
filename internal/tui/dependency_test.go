package tui

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAgentTeamdDependencyClosureIsCharmFree(t *testing.T) {
	command := exec.Command("go", "list", "-deps", "./cmd/agent-teamd")
	command.Dir = filepath.Clean("../..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, output)
	}
	var offenders []string
	for _, dependency := range strings.Fields(string(output)) {
		if strings.HasPrefix(dependency, "github.com/charmbracelet/") {
			offenders = append(offenders, dependency)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("agent-teamd Charm dependencies: %v", offenders)
	}
}

func TestCharmImportsStayInsideTUI(t *testing.T) {
	root := filepath.Clean("../..")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && (strings.HasPrefix(info.Name(), ".") || info.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		for _, spec := range file.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			if strings.HasPrefix(importPath, "github.com/charmbracelet/") && !strings.HasPrefix(filepath.ToSlash(relative), "internal/tui/") {
				t.Errorf("Charm import %q outside internal/tui: %s", importPath, relative)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCharmDependencyPins(t *testing.T) {
	body, err := os.ReadFile(filepath.Clean("../../go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []string{
		"github.com/charmbracelet/bubbletea v1.3.4",
		"github.com/charmbracelet/lipgloss v1.1.0",
		"github.com/charmbracelet/bubbles v0.20.0",
		"github.com/charmbracelet/x/exp/teatest v0.0.0-20241028122716-59f28b971972",
	} {
		if !bytes.Contains(body, []byte(pin)) {
			t.Errorf("go.mod missing exact pin %q", pin)
		}
	}
}

func TestCommandLayerHasNoMutatingDaemonCalls(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "commands.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	banned := map[string]bool{
		"Dispatch": true, "Reconcile": true, "SendMessage": true, "PublishEvent": true,
		"QueueDrop": true, "QueueRetry": true, "QueueDrain": true, "OutboxDrain": true,
		"ScheduleFire": true, "StopInstance": true, "StartInstance": true,
		"RestartInstance": true, "RemoveInstance": true, "TopologyReload": true,
		"ChannelPublish": true, "ChannelAck": true, "ChannelDelete": true,
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && banned[selector.Sel.Name] {
			t.Errorf("mutating daemon call %s is forbidden in the read-only command layer", selector.Sel.Name)
		}
		return true
	})
}
