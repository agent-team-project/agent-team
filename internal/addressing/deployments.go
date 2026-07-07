// Package addressing resolves human-friendly deployment names to resource URIs.
package addressing

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/resource"
)

const (
	DeploymentSourceSelf   = "self"
	DeploymentSourceRoute  = "route"
	DeploymentSourceParent = "parent"
)

// DeploymentEntry is one row in the read-only deployment registry view.
type DeploymentEntry struct {
	Name      string   `json:"name"`
	URI       string   `json:"uri"`
	Source    string   `json:"source"`
	Aliases   []string `json:"aliases,omitempty"`
	Root      string   `json:"root,omitempty"`
	TeamDir   string   `json:"team_dir,omitempty"`
	Transport string   `json:"transport,omitempty"`
	Endpoint  string   `json:"endpoint,omitempty"`
}

// View returns the deployment registry view for teamDir. It projects existing
// resource identity and local route state; it never creates or updates a
// registry file.
func View(teamDir string) ([]DeploymentEntry, error) {
	teamDir = strings.TrimSpace(teamDir)
	if teamDir == "" {
		return nil, errors.New("team dir is required")
	}
	teamDir, err := filepath.Abs(teamDir)
	if err != nil {
		return nil, err
	}
	teamDir = filepath.Clean(teamDir)
	current, err := currentDeploymentEntry(teamDir)
	if err != nil {
		return nil, err
	}
	cfg, err := loadDeploymentConfig(teamDir)
	if err != nil {
		return nil, err
	}
	var entries []DeploymentEntry
	if current.URI != "" {
		entries = append(entries, current)
	}
	if cfg.Project.ParentURI != "" {
		if _, err := resource.Parse(cfg.Project.ParentURI); err != nil {
			return nil, fmt.Errorf("project parent_uri: %w", err)
		}
		entries = append(entries, DeploymentEntry{
			Name:   "parent",
			URI:    cfg.Project.ParentURI,
			Source: DeploymentSourceParent,
		})
	}
	for _, entry := range routeDeploymentEntries(teamDir, cfg) {
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Source != entries[j].Source {
			return deploymentSourceRank(entries[i].Source) < deploymentSourceRank(entries[j].Source)
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// Resolve returns the canonical deployment resource URI for nameOrURI.
func Resolve(teamDir, nameOrURI string) (DeploymentEntry, error) {
	nameOrURI = strings.TrimSpace(nameOrURI)
	if nameOrURI == "" {
		return DeploymentEntry{}, errors.New("deployment name or URI is required")
	}
	if strings.HasPrefix(nameOrURI, resource.Scheme+"://") {
		if _, err := resource.Parse(nameOrURI); err != nil {
			return DeploymentEntry{}, err
		}
		return DeploymentEntry{Name: nameOrURI, URI: nameOrURI, Source: "literal"}, nil
	}
	entries, err := View(teamDir)
	if err != nil {
		return DeploymentEntry{}, err
	}
	needle := strings.ToLower(nameOrURI)
	for _, entry := range entries {
		if strings.ToLower(entry.Name) == needle || strings.ToLower(entry.URI) == needle {
			return entry, nil
		}
		for _, alias := range entry.Aliases {
			if strings.ToLower(alias) == needle {
				return entry, nil
			}
		}
	}
	return DeploymentEntry{}, fmt.Errorf("deployment %q is not in the registry view", nameOrURI)
}

type deploymentConfigFile struct {
	Project  projectConfig            `toml:"project"`
	Feedback feedbackDeploymentConfig `toml:"feedback"`
}

type projectConfig struct {
	ID        string `toml:"id"`
	ParentURI string `toml:"parent_uri"`
}

type feedbackDeploymentConfig struct {
	Routes map[string]rawDeploymentRouteConfig `toml:"routes"`
}

type rawDeploymentRouteConfig struct {
	Type string `toml:"type"`
	Kind string `toml:"kind"`
	Root string `toml:"root"`
}

func loadDeploymentConfig(teamDir string) (deploymentConfigFile, error) {
	var cfg deploymentConfigFile
	path := filepath.Join(teamDir, "config.toml")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Project.ID = strings.TrimSpace(cfg.Project.ID)
	cfg.Project.ParentURI = strings.TrimSpace(cfg.Project.ParentURI)
	return cfg, nil
}

func currentDeploymentEntry(teamDir string) (DeploymentEntry, error) {
	deployment, err := resource.DeploymentFromTeamDir(teamDir)
	if err != nil {
		return DeploymentEntry{}, err
	}
	if strings.TrimSpace(deployment.ID) == "" {
		return DeploymentEntry{}, nil
	}
	root := filepath.Dir(teamDir)
	entry := DeploymentEntry{
		Name:    "self",
		URI:     deployment.URI,
		Source:  DeploymentSourceSelf,
		Aliases: []string{"local", ".", deployment.ID},
		Root:    filepath.ToSlash(root),
		TeamDir: filepath.ToSlash(teamDir),
	}
	entry.Transport, entry.Endpoint = deploymentEndpoint(teamDir)
	return entry, nil
}

func routeDeploymentEntries(teamDir string, cfg deploymentConfigFile) []DeploymentEntry {
	if len(cfg.Feedback.Routes) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Feedback.Routes))
	for name := range cfg.Feedback.Routes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]DeploymentEntry, 0, len(names))
	for _, name := range names {
		raw := cfg.Feedback.Routes[name]
		kind := strings.ToLower(strings.TrimSpace(firstNonEmpty(raw.Type, raw.Kind)))
		if kind != "local" {
			continue
		}
		root := strings.TrimSpace(raw.Root)
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(filepath.Dir(teamDir), root)
		}
		root = filepath.Clean(root)
		routeTeamDir := filepath.Join(root, ".agent_team")
		deployment, err := resource.DeploymentFromTeamDir(routeTeamDir)
		if err != nil || strings.TrimSpace(deployment.ID) == "" || deployment.URI == "" {
			continue
		}
		entry := DeploymentEntry{
			Name:    strings.TrimSpace(name),
			URI:     deployment.URI,
			Source:  DeploymentSourceRoute,
			Aliases: []string{deployment.ID},
			Root:    filepath.ToSlash(root),
			TeamDir: filepath.ToSlash(routeTeamDir),
		}
		entry.Transport, entry.Endpoint = deploymentEndpoint(routeTeamDir)
		out = append(out, entry)
	}
	return out
}

func deploymentEndpoint(teamDir string) (string, string) {
	if url := daemon.DaemonHTTPURL(mustReadHTTPAddr(teamDir)); url != "" {
		return "http", url
	}
	socket := daemon.SocketPath(teamDir)
	if st, err := os.Stat(socket); err == nil && !st.IsDir() {
		return "unix", filepath.ToSlash(socket)
	}
	return "", ""
}

func mustReadHTTPAddr(teamDir string) string {
	addr, err := daemon.ReadHTTPAddr(teamDir)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(addr)
}

func deploymentSourceRank(source string) int {
	switch source {
	case DeploymentSourceSelf:
		return 0
	case DeploymentSourceParent:
		return 1
	case DeploymentSourceRoute:
		return 2
	default:
		return 10
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
