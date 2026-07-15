package topology

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShippedControlPlaneGatesRequireDurableActivationDisposition(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	managerPrompt := readActivationInstructionFile(t, filepath.Join(repoRoot, "template", "agents", "manager", "agent.md"))
	for _, want := range []string{"Activation disposition", "merged SHA", "activation-needed", "topology validate", "instance ps", "managed authority shim"} {
		if !strings.Contains(managerPrompt, want) {
			t.Fatalf("manager prompt missing %q", want)
		}
	}

	fragments := []string{
		"10_core_delivery_pipeline.toml.tmpl",
		"40_full_platform_pipeline.toml.tmpl",
		"70_full_release_pipeline.toml.tmpl",
		"85_full_research_program.toml.tmpl",
		"87_full_frontend_pipeline.toml.tmpl",
	}
	for _, name := range fragments {
		t.Run(name, func(t *testing.T) {
			body := readActivationInstructionFile(t, filepath.Join(repoRoot, "template", "topology", "instances.toml.tmpl.d", name))
			for _, want := range []string{"merged SHA", "activation-needed", "topology validate/reload", "instance ps", "managed shim"} {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing %q", name, want)
				}
			}
		})
	}
}

func TestShippedManagerAuthorityRemainsTeamScoped(t *testing.T) {
	body := readActivationInstructionFile(t, filepath.Join("..", "..", "template", "topology", "instances.toml.tmpl.d", "95_common_delivery_policy.toml.tmpl"))
	for _, instance := range []string{"frontend-manager", "research-manager"} {
		marker := "[authority.instances." + instance + "]"
		start := strings.Index(body, marker)
		if start < 0 {
			t.Fatalf("missing authority block %s", marker)
		}
		section := body[start:]
		if next := strings.Index(section[len(marker):], "\n["); next >= 0 {
			section = section[:len(marker)+next]
		}
		if !strings.Contains(section, ":team") {
			t.Fatalf("%s authority lost :team scope:\n%s", instance, section)
		}
	}
	if !strings.Contains(body, "[authority.instances.manager]\nallow = [\"*\"]") {
		t.Fatal("default manager authority changed while adding activation coherence")
	}
}

func readActivationInstructionFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}
