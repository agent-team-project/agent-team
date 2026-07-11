package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

type OverviewSummary struct {
	Instances     int
	Running       int
	Jobs          int
	ActiveJobs    int
	BlockedJobs   int
	FailedJobs    int
	ModelTiers    int
	BounceClasses int
	Pipelines     int
	Budgets       int
	Teams         int
	Schedules     int
	Deployments   int
	Deadlines     int
}

type AttentionRow struct {
	ID       string
	Kind     string
	Status   string
	Detail   string
	Role     string
	Ticket   string
	Severity int
}

type OrgRow struct {
	Role     string
	Working  int
	Idle     int
	Crashed  int
	Queued   int
	Running  int
	Capacity int
}

type OverviewProjection struct {
	Summary   OverviewSummary
	Attention []AttentionRow
	Org       []OrgRow
}

func projectOverview(model Model) OverviewProjection {
	var out OverviewProjection
	if model.Snapshot == nil {
		return out
	}
	snapshot := model.Snapshot
	out.Summary.Instances = len(snapshot.Instances)
	for _, instance := range snapshot.Instances {
		if instance == nil {
			continue
		}
		switch instance.Status {
		case daemonclient.InstanceRunning:
			out.Summary.Running++
		case daemonclient.InstanceCrashed:
			out.Attention = append(out.Attention, AttentionRow{
				ID: instance.Instance, Kind: "instance", Status: "crashed", Role: instance.Agent,
				Detail: exitDetail(instance.ExitCode), Severity: 4,
			})
		}
	}
	out.Summary.Jobs = len(snapshot.Jobs)
	for _, job := range snapshot.Jobs {
		if job == nil {
			continue
		}
		active := job.Status == daemonclient.JobQueued || job.Status == daemonclient.JobRunning || job.Status == daemonclient.JobBlocked
		if active {
			out.Summary.ActiveJobs++
		}
		severity := 0
		switch job.Status {
		case daemonclient.JobFailed:
			out.Summary.FailedJobs++
			severity = 5
		case daemonclient.JobBlocked:
			out.Summary.BlockedJobs++
			severity = 4
		case daemonclient.JobRunning:
			severity = 2
		case daemonclient.JobQueued:
			severity = 1
		}
		if severity > 0 {
			out.Attention = append(out.Attention, AttentionRow{
				ID: job.ID, Kind: "job", Status: string(job.Status), Detail: firstText(job.Instance, job.Pipeline, job.Target),
				Role: job.Target, Ticket: job.Ticket, Severity: severity,
			})
		}
	}
	if topology := snapshot.Topology; topology != nil {
		out.Summary.Pipelines = len(topology.Pipelines)
		out.Summary.Budgets = len(topology.Budgets)
		out.Summary.Teams = len(topology.Teams)
		out.Summary.Schedules = len(topology.Schedules)
	}
	out.Summary.ModelTiers = distinctModelTiers(snapshot)
	out.Summary.BounceClasses = distinctBounceClasses(snapshot)
	out.Summary.Deployments = distinctDeployments(snapshot)
	out.Summary.Deadlines = distinctDeadlines(snapshot)
	out.Attention = filterAttention(out.Attention, model.Query)
	sort.SliceStable(out.Attention, func(i, j int) bool {
		if out.Attention[i].Severity != out.Attention[j].Severity {
			return out.Attention[i].Severity > out.Attention[j].Severity
		}
		return out.Attention[i].ID < out.Attention[j].ID
	})
	out.Org = projectOrg(snapshot)
	return out
}

func projectOrg(snapshot *daemonclient.Snapshot) []OrgRow {
	rows := map[string]*OrgRow{}
	rowFor := func(role string) *OrgRow {
		role = strings.TrimSpace(role)
		if role == "" {
			role = "unknown"
		}
		if rows[role] == nil {
			rows[role] = &OrgRow{Role: role}
		}
		return rows[role]
	}
	for _, instance := range snapshot.Instances {
		if instance == nil {
			continue
		}
		row := rowFor(instance.Agent)
		switch instance.Status {
		case daemonclient.InstanceRunning:
			row.Working++
			row.Running++
		case daemonclient.InstanceCrashed:
			row.Crashed++
		default:
			row.Idle++
		}
	}
	if snapshot.Topology != nil {
		for _, declaration := range snapshot.Topology.Instances {
			row := rowFor(declaration.Agent)
			row.Queued += declaration.Queued
			if declaration.Replicas > 0 {
				row.Capacity += declaration.Replicas
			}
		}
	}
	out := make([]OrgRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}

func distinctModelTiers(snapshot *daemonclient.Snapshot) int {
	jobs := append([]*daemonclient.Job(nil), snapshot.Jobs...)
	sort.SliceStable(jobs, func(i, j int) bool {
		if jobs[i] == nil || jobs[j] == nil {
			return jobs[j] == nil
		}
		if !jobs[i].UpdatedAt.Equal(jobs[j].UpdatedAt) {
			return jobs[i].UpdatedAt.After(jobs[j].UpdatedAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	if len(jobs) > 24 {
		jobs = jobs[:24]
	}
	groups := map[string]bool{}
	for _, job := range jobs {
		if job == nil {
			continue
		}
		data := resourceMap(snapshot.Resources[job.OutcomeURI])
		model := recursiveString(data, "model")
		tier := recursiveString(data, "tier")
		if model == "" && tier == "" {
			groups["not reported"] = true
		} else {
			groups[firstText(model, "not reported")+"/"+firstText(tier, "not reported")] = true
		}
	}
	return len(groups)
}

func distinctBounceClasses(snapshot *daemonclient.Snapshot) int {
	classes := map[string]bool{}
	for _, job := range snapshot.Jobs {
		if job == nil {
			continue
		}
		jobClasses := map[string]bool{}
		collectBounceClasses(resourceMap(snapshot.Resources[job.OutcomeURI]), jobClasses)
		if len(jobClasses) == 0 {
			data := resourceMap(snapshot.Resources[job.URI])
			collectBounceClasses(data, jobClasses)
			if len(jobClasses) == 0 {
				collectKickoffBounceClasses(recursiveString(data, "kickoff"), jobClasses)
			}
		}
		for class := range jobClasses {
			classes[class] = true
		}
	}
	return len(classes)
}

func collectKickoffBounceClasses(kickoff string, classes map[string]bool) {
	lower := strings.ToLower(kickoff)
	if !strings.Contains(lower, "review findings (bounce") {
		return
	}
	for class, phrases := range map[string][]string{
		"capability":     {"capability", "logic error", "missing test", "incorrect", "regression"},
		"spec-ambiguity": {"spec ambiguity", "spec-ambiguity", "ambiguous", "underspecified", "clarify"},
		"scope":          {"scope", "drive-by", "unrelated", "too broad", "owned path"},
		"infra":          {"infra", "flake", "timeout", "rate limit", "credential", "network", "runner"},
	} {
		for _, phrase := range phrases {
			if strings.Contains(lower, phrase) {
				classes[class] = true
				break
			}
		}
	}
}

func collectBounceClasses(value any, classes map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if lower == "bounce_classes" || lower == "bounces" || lower == "bounceclasses" {
				switch found := child.(type) {
				case map[string]any:
					for class, count := range found {
						if numberPositive(count) {
							classes[class] = true
						}
					}
				case []any:
					for _, class := range found {
						if text, ok := class.(string); ok && strings.TrimSpace(text) != "" {
							classes[text] = true
						}
					}
				}
			}
			collectBounceClasses(child, classes)
		}
	case []any:
		for _, child := range typed {
			collectBounceClasses(child, classes)
		}
	}
}

func distinctDeployments(snapshot *daemonclient.Snapshot) int {
	deployments := map[string]bool{}
	for _, instance := range snapshot.Instances {
		if instance != nil && instance.DeploymentURI != "" {
			deployments[instance.DeploymentURI] = true
		}
	}
	for _, job := range snapshot.Jobs {
		if job != nil && job.DeploymentURI != "" {
			deployments[job.DeploymentURI] = true
		}
	}
	for _, resource := range snapshot.Resources {
		collectStringsByKey(resourceMap(resource), "deployment_uri", deployments)
	}
	return len(deployments)
}

func distinctDeadlines(snapshot *daemonclient.Snapshot) int {
	deadlines := map[string]bool{}
	for _, instance := range snapshot.Instances {
		if instance != nil && !instance.RuntimeDeadline.IsZero() {
			deadlines["instance:"+instance.Instance+":"+instance.RuntimeDeadline.UTC().String()] = true
		}
	}
	for uri, resource := range snapshot.Resources {
		collectDeadlines(resourceMap(resource), uri, deadlines)
	}
	return len(deadlines)
}

func collectDeadlines(value any, prefix string, out map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "deadline") && !strings.Contains(lower, "state") && !strings.Contains(lower, "source") {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					out[prefix+":"+text] = true
				}
			}
			collectDeadlines(child, prefix+"/"+key, out)
		}
	case []any:
		for i, child := range typed {
			collectDeadlines(child, fmt.Sprintf("%s/%d", prefix, i), out)
		}
	}
}

func collectStringsByKey(value any, wanted string, out map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.EqualFold(key, wanted) {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					out[text] = true
				}
			}
			collectStringsByKey(child, wanted, out)
		}
	case []any:
		for _, child := range typed {
			collectStringsByKey(child, wanted, out)
		}
	}
}

func resourceMap(resource *daemonclient.Resource) map[string]any {
	if resource == nil || len(resource.Data) == 0 {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(resource.Data, &out) != nil {
		return nil
	}
	return out
}

func recursiveString(value any, wanted string) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.EqualFold(key, wanted) {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					return text
				}
			}
		}
		for _, child := range typed {
			if found := recursiveString(child, wanted); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := recursiveString(child, wanted); found != "" {
				return found
			}
		}
	}
	return ""
}

func filterAttention(rows []AttentionRow, query string) []AttentionRow {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" || validateOverviewQuery(query) != "" {
		return rows
	}
	plain := []string{}
	fields := map[string][]string{}
	for _, term := range strings.Fields(query) {
		if i := strings.IndexByte(term, ':'); i > 0 {
			fields[term[:i]] = append(fields[term[:i]], term[i+1:])
		} else {
			plain = append(plain, term)
		}
	}
	out := make([]AttentionRow, 0, len(rows))
	for _, row := range rows {
		all := strings.ToLower(strings.Join([]string{row.ID, row.Kind, row.Status, row.Detail, row.Role, row.Ticket}, " "))
		match := true
		for _, term := range plain {
			match = match && strings.Contains(all, term)
		}
		values := map[string]string{"id": row.ID, "status": row.Status, "type": row.Kind, "role": row.Role, "ticket": row.Ticket}
		for field, wants := range fields {
			fieldMatch := false
			for _, want := range wants {
				fieldMatch = fieldMatch || strings.Contains(strings.ToLower(values[field]), want)
			}
			match = match && fieldMatch
		}
		if match {
			out = append(out, row)
		}
	}
	return out
}

func exitDetail(code *int) string {
	if code == nil {
		return "process exited unexpectedly"
	}
	return fmt.Sprintf("exit %d", *code)
}

func numberPositive(value any) bool {
	switch number := value.(type) {
	case float64:
		return number > 0
	case int:
		return number > 0
	case json.Number:
		parsed, _ := number.Float64()
		return parsed > 0
	default:
		return value != nil
	}
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "-"
}
