package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/BurntSushi/toml"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

type jobDoctorFinding struct {
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type jobDoctorSummary struct {
	Files   int `json:"files"`
	Jobs    int `json:"jobs"`
	Valid   int `json:"valid"`
	Invalid int `json:"invalid"`
	Ignored int `json:"ignored"`
}

type jobDoctorResult struct {
	OK       bool               `json:"ok"`
	Root     string             `json:"root"`
	Summary  jobDoctorSummary   `json:"summary"`
	Problems []jobDoctorFinding `json:"problems,omitempty"`
	Warnings []jobDoctorFinding `json:"warnings,omitempty"`
	Actions  []string           `json:"actions,omitempty"`
}

func newJobDoctorCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate durable job files.",
		Long:  "Validate durable job TOML files under `.agent_team/jobs/` without relying on normal job listing paths.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job doctor: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := collectJobDoctor(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job doctor: %v\n", err)
				return exitErr(1)
			}
			if err := renderJobDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, tmpl); err != nil {
				return err
			}
			if !result.OK {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit durable job doctor findings as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job doctor result with a Go template, e.g. '{{.OK}} {{.Summary.Valid}}'.")
	return cmd
}

func collectJobDoctor(teamDir string) (jobDoctorResult, error) {
	root := job.Directory(teamDir)
	result := jobDoctorResult{
		OK:   true,
		Root: root,
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	seen := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".toml") {
			result.Summary.Ignored++
			continue
		}
		result.Summary.Files++
		id := strings.TrimSuffix(name, ".toml")
		path := filepath.Join(root, name)
		fileHasProblem := false

		if strings.TrimSpace(id) == "" {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				Path:    path,
				Code:    "invalid_filename",
				Message: fmt.Sprintf("%s has an empty job id in its filename", path),
			})
			fileHasProblem = true
		} else if normalized := job.NormalizeID(id); normalized != id {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				ID:      id,
				Path:    path,
				Code:    "filename_not_normalized",
				Message: fmt.Sprintf("%s filename id %q must be normalized as %q", path, id, normalized),
			})
			fileHasProblem = true
		}

		var j job.Job
		if _, err := toml.DecodeFile(path, &j); err != nil {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				ID:      id,
				Path:    path,
				Code:    "invalid_toml",
				Message: fmt.Sprintf("%s is not valid job TOML: %v", path, err),
			})
			result.Summary.Invalid++
			continue
		}
		result.Summary.Jobs++
		if j.ID == "" {
			j.ID = id
		}
		if err := job.Validate(&j); err != nil {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				ID:      j.ID,
				Path:    path,
				Code:    "invalid_job",
				Message: fmt.Sprintf("%s has invalid job data: %v", path, err),
			})
			fileHasProblem = true
		}
		if j.ID != "" && j.ID != id {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				ID:      j.ID,
				Path:    path,
				Code:    "id_mismatch",
				Message: fmt.Sprintf("%s filename id %q does not match job id %q", path, id, j.ID),
			})
			fileHasProblem = true
		}
		if j.ID != "" {
			if previous, ok := seen[j.ID]; ok {
				jobDoctorAddProblem(&result, jobDoctorFinding{
					ID:      j.ID,
					Path:    path,
					Code:    "duplicate_id",
					Message: fmt.Sprintf("%s duplicates job id %q already used by %s", path, j.ID, previous),
				})
				fileHasProblem = true
			} else {
				seen[j.ID] = path
			}
		}
		if fileHasProblem {
			result.Summary.Invalid++
		} else {
			result.Summary.Valid++
		}
	}
	result.OK = len(result.Problems) == 0
	result.Actions = jobDoctorActions(result)
	sortJobDoctorFindings(result.Problems)
	sortJobDoctorFindings(result.Warnings)
	return result, nil
}

func jobDoctorAddProblem(result *jobDoctorResult, finding jobDoctorFinding) {
	result.Problems = append(result.Problems, finding)
}

func jobDoctorActions(result jobDoctorResult) []string {
	if len(result.Problems) == 0 {
		return nil
	}
	return []string{"agent-team job doctor --json", "agent-team snapshot --json"}
}

func renderJobDoctor(stdout, stderr io.Writer, result jobDoctorResult, jsonOut bool, tmpl *template.Template) error {
	sortJobDoctorFindings(result.Problems)
	sortJobDoctorFindings(result.Warnings)
	if jsonOut {
		return json.NewEncoder(stdout).Encode(result)
	}
	if tmpl != nil {
		return renderJobDoctorFormat(stdout, result, tmpl)
	}
	if result.OK {
		fmt.Fprintln(stdout, "agent-team job doctor: OK")
		renderJobDoctorSummary(stdout, result.Summary)
		for _, warning := range result.Warnings {
			fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
		}
		return nil
	}
	fmt.Fprintln(stderr, "agent-team job doctor: problems found:")
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "  - %s\n", problem.Message)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
	}
	if len(result.Actions) > 0 {
		fmt.Fprintln(stderr, "next actions:")
		for _, action := range result.Actions {
			fmt.Fprintf(stderr, "  - %s\n", action)
		}
	}
	return nil
}

func renderJobDoctorSummary(w io.Writer, summary jobDoctorSummary) {
	fmt.Fprintf(w, "jobs: files=%d valid=%d invalid=%d ignored=%d\n", summary.Files, summary.Valid, summary.Invalid, summary.Ignored)
}

func parseJobDoctorFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-doctor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderJobDoctorFormat(w io.Writer, result jobDoctorResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func sortJobDoctorFindings(findings []jobDoctorFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		if findings[i].Code != findings[j].Code {
			return findings[i].Code < findings[j].Code
		}
		return findings[i].ID < findings[j].ID
	})
}
