// Package origin stores and renders provenance for agent-team resources.
package origin

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/resource"
)

// HeaderName carries the actor origin on daemon HTTP requests.
const HeaderName = "Agent-Team-Origin"

// Envelope identifies where a resource came from and who owns it.
type Envelope struct {
	Project       string `json:"project,omitempty" toml:"project,omitempty"`
	DeploymentURI string `json:"deployment_uri,omitempty" toml:"deployment_uri,omitempty"`
	Team          string `json:"team,omitempty" toml:"team,omitempty"`
	Instance      string `json:"instance,omitempty" toml:"instance,omitempty"`
	InstanceURI   string `json:"instance_uri,omitempty" toml:"instance_uri,omitempty"`
	Agent         string `json:"agent,omitempty" toml:"agent,omitempty"`
	Job           string `json:"job,omitempty" toml:"job,omitempty"`
	JobURI        string `json:"job_uri,omitempty" toml:"job_uri,omitempty"`
	Trigger       string `json:"trigger,omitempty" toml:"trigger,omitempty"`
	Build         string `json:"build,omitempty" toml:"build,omitempty"`
}

// Clean trims whitespace from every field.
func (e Envelope) Clean() Envelope {
	return Envelope{
		Project:       strings.TrimSpace(e.Project),
		DeploymentURI: strings.TrimSpace(e.DeploymentURI),
		Team:          strings.TrimSpace(e.Team),
		Instance:      strings.TrimSpace(e.Instance),
		InstanceURI:   strings.TrimSpace(e.InstanceURI),
		Agent:         strings.TrimSpace(e.Agent),
		Job:           strings.TrimSpace(e.Job),
		JobURI:        strings.TrimSpace(e.JobURI),
		Trigger:       strings.TrimSpace(e.Trigger),
		Build:         strings.TrimSpace(e.Build),
	}
}

// Empty reports whether no origin fields are populated.
func (e Envelope) Empty() bool {
	e = e.Clean()
	return e.Project == "" && e.DeploymentURI == "" && e.Team == "" &&
		e.Instance == "" && e.InstanceURI == "" && e.Agent == "" &&
		e.Job == "" && e.JobURI == "" && e.Trigger == "" && e.Build == ""
}

// WithResourceURIs backfills canonical agt:// resource URIs from the stable
// ids already present in the envelope. It is intentionally additive: callers
// can keep using the scalar fields while cross-deployment consumers join on
// the URI fields.
func (e Envelope) WithResourceURIs() Envelope {
	e = e.Clean()
	if e.Project == "" {
		return e
	}
	if e.DeploymentURI == "" {
		e.DeploymentURI = resource.ProjectURI(e.Project)
	}
	if e.Instance != "" && e.InstanceURI == "" {
		e.InstanceURI = resource.InstanceURI(e.Project, e.Instance)
	}
	if e.Job != "" && e.JobURI == "" {
		e.JobURI = resource.JobURI(e.Project, e.Job)
	}
	return e
}

// Merge fills blank fields in primary from fallback.
func Merge(primary, fallback Envelope) Envelope {
	primary = primary.Clean()
	fallback = fallback.Clean()
	if primary.Project == "" {
		primary.Project = fallback.Project
	}
	if primary.Team == "" {
		primary.Team = fallback.Team
	}
	if primary.DeploymentURI == "" {
		primary.DeploymentURI = fallback.DeploymentURI
	}
	if primary.Instance == "" {
		primary.Instance = fallback.Instance
	}
	if primary.InstanceURI == "" {
		primary.InstanceURI = fallback.InstanceURI
	}
	if primary.Agent == "" {
		primary.Agent = fallback.Agent
	}
	if primary.Job == "" {
		primary.Job = fallback.Job
	}
	if primary.JobURI == "" {
		primary.JobURI = fallback.JobURI
	}
	if primary.Trigger == "" {
		primary.Trigger = fallback.Trigger
	}
	if primary.Build == "" {
		primary.Build = fallback.Build
	}
	return primary.WithResourceURIs()
}

// Footer renders the machine-parseable footer used on external writes.
func Footer(e Envelope) string {
	value := HeaderValue(e)
	if value == "" {
		return ""
	}
	return "agent-team-origin: " + value
}

// HeaderValue renders the machine-parseable origin fields for HeaderName.
func HeaderValue(e Envelope) string {
	e = e.WithResourceURIs()
	if e.Empty() {
		return ""
	}
	parts := []string{}
	for _, item := range []struct {
		key   string
		value string
	}{
		{"project", e.Project},
		{"deployment_uri", e.DeploymentURI},
		{"team", e.Team},
		{"instance", e.Instance},
		{"instance_uri", e.InstanceURI},
		{"agent", e.Agent},
		{"job", e.Job},
		{"job_uri", e.JobURI},
		{"trigger", e.Trigger},
		{"build", e.Build},
	} {
		if item.value != "" {
			parts = append(parts, item.key+"="+quoteFooterValue(item.value))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

// ParseHeaderValue decodes HeaderName or a footer-style origin value.
func ParseHeaderValue(raw string) (Envelope, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Envelope{}, nil
	}
	if strings.HasPrefix(strings.ToLower(raw), "agent-team-origin:") {
		raw = strings.TrimSpace(raw[len("agent-team-origin:"):])
	}
	fields, err := parseOriginFields(raw)
	if err != nil {
		return Envelope{}, err
	}
	var out Envelope
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return Envelope{}, fmt.Errorf("origin: invalid field %q", field)
		}
		switch key {
		case "project":
			out.Project = value
		case "deployment_uri":
			out.DeploymentURI = value
		case "team":
			out.Team = value
		case "instance":
			out.Instance = value
		case "instance_uri":
			out.InstanceURI = value
		case "agent":
			out.Agent = value
		case "job":
			out.Job = value
		case "job_uri":
			out.JobURI = value
		case "trigger":
			out.Trigger = value
		case "build":
			out.Build = value
		default:
			return Envelope{}, fmt.Errorf("origin: unknown field %q", key)
		}
	}
	return out.WithResourceURIs(), nil
}

func parseOriginFields(raw string) ([]string, error) {
	fields := []string{}
	for i := 0; i < len(raw); {
		for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
			i++
		}
		if i >= len(raw) {
			break
		}
		keyStart := i
		for i < len(raw) && raw[i] != '=' && raw[i] != ' ' && raw[i] != '\t' {
			i++
		}
		if i >= len(raw) || raw[i] != '=' {
			return nil, fmt.Errorf("origin: expected key=value")
		}
		key := raw[keyStart:i]
		i++
		if i < len(raw) && raw[i] == '"' {
			valueStart := i
			i++
			escaped := false
			closed := false
			for i < len(raw) {
				c := raw[i]
				i++
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if c == '"' {
					quoted := raw[valueStart:i]
					value, err := strconv.Unquote(quoted)
					if err != nil {
						return nil, fmt.Errorf("origin: invalid quoted value for %s: %w", key, err)
					}
					fields = append(fields, key+"="+value)
					closed = true
					break
				}
			}
			if !closed {
				return nil, fmt.Errorf("origin: unterminated quoted value for %s", key)
			}
			continue
		}
		valueStart := i
		for i < len(raw) && raw[i] != ' ' && raw[i] != '\t' {
			i++
		}
		fields = append(fields, key+"="+raw[valueStart:i])
	}
	return fields, nil
}

// AppendFooter adds the provenance footer unless one is already present.
func AppendFooter(body string, e Envelope) string {
	footer := Footer(e)
	body = strings.TrimRight(body, "\n")
	if footer == "" || strings.Contains(body, "agent-team-origin:") {
		return body
	}
	if body == "" {
		return footer
	}
	return body + "\n\n---\n" + footer
}

func quoteFooterValue(value string) string {
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return strconv.Quote(value)
	}
	return value
}

// TriggerFromEvent returns the stable trigger id for a topology event payload.
func TriggerFromEvent(eventType string, payload map[string]any) string {
	eventType = strings.TrimSpace(eventType)
	if eventType == "schedule" || payloadString(payload, "source") == "schedule" {
		if name := payloadString(payload, "name"); name != "" {
			return "schedule:" + name
		}
	}
	if pipeline := payloadString(payload, "pipeline"); pipeline != "" {
		if step := payloadString(payload, "pipeline_step"); step != "" {
			return "pipeline:" + pipeline + ":" + step
		}
		return "pipeline:" + pipeline
	}
	if source := payloadString(payload, "source"); source != "" && eventType == "" {
		return source
	}
	return eventType
}

// ConfigPath returns the repo-local config path for a team directory.
func ConfigPath(teamDir string) string {
	return filepath.Join(teamDir, "config.toml")
}

// ProjectID reads [project].id from config.toml. Missing config or key returns
// an empty id with no error.
func ProjectID(teamDir string) (string, error) {
	cfg := ConfigPath(teamDir)
	if _, err := os.Stat(cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var raw map[string]any
	if _, err := toml.DecodeFile(cfg, &raw); err != nil {
		return "", err
	}
	project, _ := raw["project"].(map[string]any)
	id, _ := project["id"].(string)
	return strings.TrimSpace(id), nil
}

// EnsureProjectID creates [project].id when it is missing or empty.
func EnsureProjectID(teamDir string) (string, bool, error) {
	if id, err := ProjectID(teamDir); err != nil {
		return "", false, err
	} else if id != "" {
		return id, false, nil
	}
	id, err := GenerateProjectID()
	if err != nil {
		return "", false, err
	}
	if err := WriteProjectID(teamDir, id); err != nil {
		return "", false, err
	}
	return id, true, nil
}

// GenerateProjectID returns a stable random project id suitable for config.
func GenerateProjectID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexed[:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:]), nil
}

// WriteProjectID inserts or replaces [project].id while preserving the rest of
// the config file.
func WriteProjectID(teamDir, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("origin: project id is required")
	}
	cfg := ConfigPath(teamDir)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		return err
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(cfg, []byte("[project]\nid = "+strconv.Quote(id)+"\n"), 0o644)
		}
		return err
	}
	var raw map[string]any
	if _, err := toml.Decode(string(body), &raw); err != nil {
		return err
	}
	updated := upsertProjectIDText(string(body), id)
	return os.WriteFile(cfg, []byte(updated), 0o644)
}

func upsertProjectIDText(body, id string) string {
	lines := strings.SplitAfter(body, "\n")
	projectStart := -1
	projectEnd := len(lines)
	sectionRE := regexp.MustCompile(`^\s*\[[^\]]+\]\s*(?:#.*)?$`)
	projectRE := regexp.MustCompile(`^\s*\[project\]\s*(?:#.*)?$`)
	idRE := regexp.MustCompile(`^\s*id\s*=`)
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if projectStart == -1 {
			if projectRE.MatchString(trimmed) {
				projectStart = i
			}
			continue
		}
		if sectionRE.MatchString(trimmed) {
			projectEnd = i
			break
		}
	}
	idLine := "id = " + strconv.Quote(id) + "\n"
	if projectStart == -1 {
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if strings.TrimSpace(body) != "" {
			body += "\n"
		}
		return body + "[project]\n" + idLine
	}
	for i := projectStart + 1; i < projectEnd; i++ {
		if idRE.MatchString(strings.TrimRight(lines[i], "\r\n")) {
			lines[i] = idLine
			return strings.Join(lines, "")
		}
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:projectStart+1]...)
	out = append(out, idLine)
	out = append(out, lines[projectStart+1:]...)
	return strings.Join(out, "")
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	switch v := payload[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		if v == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
