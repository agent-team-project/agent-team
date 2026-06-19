package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

const queueQuarantineDir = "quarantine"

type queueQuarantineItem struct {
	Path        string    `json:"path"`
	State       string    `json:"state,omitempty"`
	ID          string    `json:"id,omitempty"`
	EventType   string    `json:"event_type,omitempty"`
	Instance    string    `json:"instance,omitempty"`
	InstanceID  string    `json:"instance_id,omitempty"`
	Job         string    `json:"job,omitempty"`
	RestorePath string    `json:"restore_path,omitempty"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	Restorable  bool      `json:"restorable"`
	Problem     string    `json:"problem,omitempty"`
}

type queueQuarantineRestoreResult struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
	State       string `json:"state,omitempty"`
	ID          string `json:"id,omitempty"`
	Action      string `json:"action"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Overwrite   bool   `json:"overwrite,omitempty"`
}

func newQueueQuarantineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quarantine",
		Short: "Inspect and restore quarantined queue files.",
		Long:  "Inspect queue files moved under `.agent_team/daemon/queue/quarantine/` and restore validated entries to the active queue.",
	}
	cmd.AddCommand(newQueueQuarantineLsCmd())
	cmd.AddCommand(newQueueQuarantineRestoreCmd())
	return cmd
}

func newQueueQuarantineLsCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List quarantined queue files.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			items, err := listQueueQuarantine(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine ls: %v\n", err)
				return exitErr(1)
			}
			return renderQueueQuarantineList(cmd.OutOrStdout(), items, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit quarantined queue files as JSON.")
	return cmd
}

func newQueueQuarantineRestoreCmd() *cobra.Command {
	var (
		target  string
		dryRun  bool
		force   bool
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restore <quarantine-path>",
		Short: "Restore one validated quarantined queue file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := restoreQueueQuarantine(teamDir, args[0], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine restore: %v\n", err)
				return exitErr(1)
			}
			return renderQueueQuarantineRestore(cmd.OutOrStdout(), result, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active queue file with the same restore path.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	return cmd
}

func listQueueQuarantine(teamDir string) ([]queueQuarantineItem, error) {
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	root := filepath.Join(queueRoot, queueQuarantineDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	var items []queueQuarantineItem
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		rel, err := filepath.Rel(queueRoot, path)
		if err != nil {
			return err
		}
		item, err := inspectQueueQuarantineFile(queueRoot, rel)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Path < items[j].Path
	})
	return items, nil
}

func inspectQueueQuarantineFile(queueRoot, rel string) (queueQuarantineItem, error) {
	source, err := queueDoctorSafeQueuePath(queueRoot, rel)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	item := queueQuarantineItem{
		Path:    filepath.Clean(rel),
		State:   queueQuarantineState(rel),
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}
	if item.State != "" {
		item.RestorePath = filepath.Join(item.State, filepath.Base(item.Path))
	}
	body, err := os.ReadFile(source)
	if err != nil {
		item.Problem = err.Error()
		return item, nil
	}
	var raw daemon.QueueItem
	if err := json.Unmarshal(body, &raw); err != nil {
		item.Problem = fmt.Sprintf("invalid JSON: %v", err)
		return item, nil
	}
	idFromPath := strings.TrimSuffix(filepath.Base(item.Path), ".json")
	if strings.TrimSpace(raw.ID) == "" {
		raw.ID = idFromPath
	}
	raw.State = item.State
	item.ID = raw.ID
	item.EventType = raw.EventType
	item.Instance = raw.Instance
	item.InstanceID = raw.InstanceID
	item.Job = queueQuarantineJob(raw.Payload)
	if err := validateQueueQuarantineRestore(raw); err != nil {
		item.Problem = err.Error()
		return item, nil
	}
	item.Restorable = true
	return item, nil
}

func queueQuarantineState(rel string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) < 4 || parts[0] != queueQuarantineDir {
		return ""
	}
	switch parts[2] {
	case daemon.QueueStatePending, daemon.QueueStateDead:
		return parts[2]
	default:
		return ""
	}
}

func queueQuarantineJob(payload map[string]any) string {
	for _, key := range []string{"job_id", "job", "ticket"} {
		if id := job.NormalizeID(queuePayloadString(payload, key)); id != "" {
			return id
		}
	}
	return ""
}

func validateQueueQuarantineRestore(item daemon.QueueItem) error {
	switch item.State {
	case daemon.QueueStatePending, daemon.QueueStateDead:
	default:
		return fmt.Errorf("queue state is required in quarantine path")
	}
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(item.EventType) == "" {
		return fmt.Errorf("event_type is required")
	}
	if strings.TrimSpace(item.Instance) == "" {
		return fmt.Errorf("instance is required")
	}
	if strings.TrimSpace(item.InstanceID) == "" {
		return fmt.Errorf("instance_id is required")
	}
	if item.QueuedAt.IsZero() {
		return fmt.Errorf("queued_at is required")
	}
	if item.UpdatedAt.IsZero() {
		return fmt.Errorf("updated_at is required")
	}
	return nil
}

func restoreQueueQuarantine(teamDir, rawPath string, dryRun, force bool) (queueQuarantineRestoreResult, error) {
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	rel, err := normalizeQueueQuarantinePath(rawPath)
	if err != nil {
		return queueQuarantineRestoreResult{}, err
	}
	item, err := inspectQueueQuarantineFile(queueRoot, rel)
	if err != nil {
		return queueQuarantineRestoreResult{}, err
	}
	if !item.Restorable {
		return queueQuarantineRestoreResult{}, fmt.Errorf("%s is not restorable: %s", item.Path, item.Problem)
	}
	source, err := queueDoctorSafeQueuePath(queueRoot, item.Path)
	if err != nil {
		return queueQuarantineRestoreResult{}, err
	}
	destination, err := queueDoctorSafeQueuePath(queueRoot, item.RestorePath)
	if err != nil {
		return queueQuarantineRestoreResult{}, err
	}
	if _, err := os.Stat(destination); err == nil && !force {
		return queueQuarantineRestoreResult{}, fmt.Errorf("%s already exists; pass --force to overwrite it", item.RestorePath)
	} else if err != nil && !os.IsNotExist(err) {
		return queueQuarantineRestoreResult{}, err
	}
	result := queueQuarantineRestoreResult{
		Path:        item.Path,
		Destination: item.RestorePath,
		State:       item.State,
		ID:          item.ID,
		Action:      "would_restore",
		DryRun:      dryRun,
		Overwrite:   force,
	}
	if dryRun {
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return result, err
	}
	if force {
		_ = os.Remove(destination)
	}
	if err := os.Rename(source, destination); err != nil {
		return result, err
	}
	result.Action = "restored"
	result.DryRun = false
	return result, nil
}

func normalizeQueueQuarantinePath(raw string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(raw))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe quarantine path %q", raw)
	}
	slash := filepath.ToSlash(clean)
	if !strings.HasPrefix(slash, queueQuarantineDir+"/") {
		slash = queueQuarantineDir + "/" + slash
	}
	if queueQuarantineState(filepath.FromSlash(slash)) == "" {
		return "", fmt.Errorf("quarantine path must look like quarantine/<timestamp>/pending/<file>.json or quarantine/<timestamp>/dead/<file>.json")
	}
	if !strings.HasSuffix(slash, ".json") {
		return "", fmt.Errorf("quarantine path must name a .json file")
	}
	return filepath.FromSlash(slash), nil
}

func renderQueueQuarantineList(w io.Writer, items []queueQuarantineItem, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(items)
	}
	if len(items) == 0 {
		fmt.Fprintln(w, "(no quarantined queue files)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PATH\tSTATE\tID\tINSTANCE\tEVENT\tJOB\tRESTORABLE\tPROBLEM")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Path,
			emptyDash(item.State),
			emptyDash(item.ID),
			emptyDash(item.Instance),
			emptyDash(item.EventType),
			emptyDash(item.Job),
			queueQuarantineRestorableText(item.Restorable),
			emptyDash(item.Problem))
	}
	return tw.Flush()
}

func queueQuarantineRestorableText(restorable bool) string {
	if restorable {
		return "yes"
	}
	return "no"
}

func renderQueueQuarantineRestore(w io.Writer, result queueQuarantineRestoreResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	switch result.Action {
	case "would_restore":
		fmt.Fprintf(w, "Would restore %s -> %s\n", result.Path, result.Destination)
	default:
		fmt.Fprintf(w, "Restored %s -> %s\n", result.Path, result.Destination)
	}
	return nil
}
