package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

// newChannelCmd builds `agent-team channel` — the resource group for the
// daemon-managed pub/sub channels (SQU-26). Mirrors the shape of `instance`.
func newChannelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channel",
		Short: "Manage daemon-managed pub/sub channels.",
	}
	cmd.AddCommand(newChannelLsCmd())
	cmd.AddCommand(newChannelShowCmd())
	cmd.AddCommand(newChannelPublishCmd())
	cmd.AddCommand(newChannelRmCmd())
	return cmd
}

// newChannelsCmd is the top-level alias `agent-team channels`, mirroring the
// `agent-team ps` shortcut for `instance ps`.
func newChannelsCmd() *cobra.Command {
	var opts channelListCommandOptions
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "channels",
		Short: "List all pub/sub channels (alias for `channel ls`).",
		RunE: func(cmd *cobra.Command, args []string) error {
			listOpts, err := channelListOptionsFromCommand(cmd, opts, "agent-team channels")
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, opts.Target)
			if err != nil {
				return err
			}
			return runChannelLs(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, listOpts)
		},
	}
	addChannelListFlags(cmd, &opts, cwd)
	return cmd
}

func newChannelLsCmd() *cobra.Command {
	var opts channelListCommandOptions
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List all channels: subscriber count, message count, last activity.",
		RunE: func(cmd *cobra.Command, args []string) error {
			listOpts, err := channelListOptionsFromCommand(cmd, opts, "agent-team channel ls")
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, opts.Target)
			if err != nil {
				return err
			}
			return runChannelLs(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, listOpts)
		},
	}
	addChannelListFlags(cmd, &opts, cwd)
	return cmd
}

func newChannelShowCmd() *cobra.Command {
	var (
		target  string
		tail    int
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one channel's summary plus its tail of recent messages.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team channel show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if tail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team channel show: --tail must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseChannelFormat(format, "channel-show-format")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team channel show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runChannelShow(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, args[0], channelShowOptions{
				Tail:   tail,
				JSON:   jsonOut,
				Format: tmpl,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().IntVar(&tail, "tail", 10, "Show at most this many recent messages; 0 means all messages.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the channel summary and messages with a Go template, e.g. '{{.Channel.Name}} {{len .Messages}}'.")
	return cmd
}

func newChannelPublishCmd() *cobra.Command {
	var (
		target      string
		sender      string
		message     string
		messageFile string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "publish <name> [body...]",
		Short: "Publish a message to a channel from the CLI (creates the channel if missing).",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			body, err := messageBodyWithFlagNames(message, messageFile, args[1:], "--message", "--message-file")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team channel publish: %v\n", err)
				return exitErr(2)
			}
			return runChannelPublish(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, args[0], sender, body)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&sender, "sender", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to publish.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	return cmd
}

func newChannelRmCmd() *cobra.Command {
	var (
		target string
		force  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a channel and all of its on-disk state.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if !force {
				ok, err := confirm(cmd, fmt.Sprintf("Delete channel %s?", args[0]))
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(cmd.OutOrStdout(), "(aborted)")
					return nil
				}
			}
			return runChannelRm(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, args[0])
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation.")
	return cmd
}

// --- run* helpers --------------------------------------------------------

type channelListCommandOptions struct {
	Target string
	Sort   string
	Limit  int
	JSON   bool
	Format string
}

type channelListOptions struct {
	Sort   string
	Limit  int
	JSON   bool
	Format *template.Template
}

type channelShowOptions struct {
	Tail   int
	JSON   bool
	Format *template.Template
}

type channelShowResult struct {
	Channel  *channelInfo      `json:"channel"`
	Messages []*channelMessage `json:"messages"`
}

func addChannelListFlags(cmd *cobra.Command, opts *channelListCommandOptions, defaultTarget string) {
	cmd.Flags().StringVar(&opts.Target, "target", defaultTarget, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&opts.Sort, "sort", "name", "Sort channels by name, subscribers, messages, or last.")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "Limit channels after sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&opts.Format, "format", "", "Render each channel with a Go template, e.g. '{{.Name}} {{.MessageCount}}'.")
}

func channelListOptionsFromCommand(cmd *cobra.Command, opts channelListCommandOptions, label string) (channelListOptions, error) {
	if opts.Format != "" && opts.JSON {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --format cannot be combined with --json.\n", label)
		return channelListOptions{}, exitErr(2)
	}
	if opts.Limit < 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --limit must be >= 0.\n", label)
		return channelListOptions{}, exitErr(2)
	}
	sortMode, err := parseChannelListSort(opts.Sort)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return channelListOptions{}, exitErr(2)
	}
	tmpl, err := parseChannelFormat(opts.Format, "channel-list-format")
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return channelListOptions{}, exitErr(2)
	}
	return channelListOptions{
		Sort:   sortMode,
		Limit:  opts.Limit,
		JSON:   opts.JSON,
		Format: tmpl,
	}, nil
}

func runChannelLs(stdout, stderr io.Writer, teamDir string, opts channelListOptions) error {
	client, err := channelClientForTeamDir(teamDir)
	if err != nil {
		return err
	}
	infos, err := client.ChannelList()
	if err != nil {
		return err
	}
	sortChannelInfos(infos, opts.Sort)
	infos = limitChannelInfos(infos, opts.Limit)
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(infos)
	}
	if opts.Format != nil {
		for _, info := range infos {
			if err := opts.Format.Execute(stdout, info); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(stdout); err != nil {
				return err
			}
		}
		return nil
	}
	if len(infos) == 0 {
		fmt.Fprintln(stdout, "(no channels)")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHANNEL\tSUBSCRIBERS\tMESSAGES\tLAST")
	now := time.Now()
	for _, info := range infos {
		last := "—"
		if !info.LastMessageTS.IsZero() {
			last = humanAge(now.Sub(info.LastMessageTS))
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", info.Name, info.Subscribers, info.MessageCount, last)
	}
	return tw.Flush()
}

const (
	channelListSortName        = "name"
	channelListSortSubscribers = "subscribers"
	channelListSortMessages    = "messages"
	channelListSortLast        = "last"
)

func parseChannelListSort(raw string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", channelListSortName, "channel":
		return channelListSortName, nil
	case channelListSortSubscribers:
		return channelListSortSubscribers, nil
	case channelListSortMessages:
		return channelListSortMessages, nil
	case channelListSortLast:
		return channelListSortLast, nil
	default:
		return "", fmt.Errorf("--sort must be name, subscribers, messages, or last")
	}
}

func sortChannelInfos(infos []*channelInfo, sortBy string) {
	sort.SliceStable(infos, func(i, j int) bool {
		left, right := infos[i], infos[j]
		if left == nil || right == nil {
			return right != nil
		}
		tie := func() bool {
			return left.Name < right.Name
		}
		switch sortBy {
		case channelListSortSubscribers:
			if left.Subscribers != right.Subscribers {
				return left.Subscribers > right.Subscribers
			}
			return tie()
		case channelListSortMessages:
			if left.MessageCount != right.MessageCount {
				return left.MessageCount > right.MessageCount
			}
			return tie()
		case channelListSortLast:
			if left.LastMessageTS.IsZero() != right.LastMessageTS.IsZero() {
				return !left.LastMessageTS.IsZero()
			}
			if !left.LastMessageTS.Equal(right.LastMessageTS) {
				return left.LastMessageTS.After(right.LastMessageTS)
			}
			return tie()
		default:
			return tie()
		}
	})
}

func limitChannelInfos(infos []*channelInfo, limit int) []*channelInfo {
	if limit <= 0 || len(infos) <= limit {
		return infos
	}
	return infos[:limit]
}

func parseChannelFormat(format, name string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New(name).Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func runChannelShow(stdout, stderr io.Writer, teamDir, name string, opts channelShowOptions) error {
	client, err := channelClientForTeamDir(teamDir)
	if err != nil {
		return err
	}
	infos, err := client.ChannelList()
	if err != nil {
		return err
	}
	var info *channelInfo
	for _, i := range infos {
		if i.Name == name {
			info = i
			break
		}
	}
	if info == nil {
		fmt.Fprintf(stderr, "agent-team: no such channel: %s\n", name)
		return exitErr(2)
	}
	// Tail recent messages by querying since=0 then keeping the tail. We pass
	// a synthetic instance label "(cli-show)" with
	// since=0 — the server doesn't require subscription when since is given.
	// A real subscriber's cursor is unaffected.
	since := int64(0)
	dr, err := client.ChannelDrain(context.Background(), name, "(cli-show)", &since, 0)
	if err != nil {
		return err
	}
	tail := dr.Messages
	if opts.Tail > 0 && len(tail) > opts.Tail {
		tail = tail[len(tail)-opts.Tail:]
	}
	result := channelShowResult{
		Channel:  info,
		Messages: tail,
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(result)
	}
	if opts.Format != nil {
		if err := opts.Format.Execute(stdout, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout)
		return err
	}
	fmt.Fprintf(stdout, "channel:       %s\n", info.Name)
	fmt.Fprintf(stdout, "subscribers:   %d\n", info.Subscribers)
	fmt.Fprintf(stdout, "messages:      %d\n", info.MessageCount)
	if !info.LastMessageTS.IsZero() {
		fmt.Fprintf(stdout, "last message:  %s\n", info.LastMessageTS.Format(time.RFC3339))
	} else {
		fmt.Fprintln(stdout, "last message:  —")
	}
	if len(tail) == 0 {
		return nil
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "recent (%d shown):\n", len(tail))
	for _, m := range tail {
		fmt.Fprintf(stdout, "  [seq=%d] %s  %s\n     %s\n",
			m.Seq, m.Sender, m.TS.Format(time.RFC3339), m.Body)
	}
	return nil
}

func runChannelPublish(stdout, stderr io.Writer, teamDir, name, sender, body string) error {
	client, err := channelClientForTeamDir(teamDir)
	if err != nil {
		return err
	}
	res, err := client.ChannelPublish(name, sender, body)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  published seq=%d  %s\n", res.Seq, res.TS.Format(time.RFC3339))
	return nil
}

func runChannelRm(stdout, stderr io.Writer, teamDir, name string) error {
	client, err := channelClientForTeamDir(teamDir)
	if err != nil {
		return err
	}
	if err := client.ChannelDelete(name); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  removed %s\n", name)
	return nil
}

type channelClient interface {
	ChannelList() ([]*channelInfo, error)
	ChannelDrain(ctx context.Context, name, instance string, since *int64, wait time.Duration) (*drainResp, error)
	ChannelPublish(name, sender, body string) (*publishResp, error)
	ChannelDelete(name string) error
}

func channelClientForTeamDir(teamDir string) (channelClient, error) {
	client, err := newDaemonClient(teamDir)
	if err == nil {
		return client, nil
	}
	if errors.Is(err, errDaemonNotRunning) {
		return localChannelClient{store: daemon.NewChannelStore(daemon.DaemonRoot(teamDir))}, nil
	}
	return nil, err
}

type localChannelClient struct {
	store *daemon.ChannelStore
}

func (c localChannelClient) ChannelList() ([]*channelInfo, error) {
	infos, err := c.store.List()
	if err != nil {
		return nil, err
	}
	out := make([]*channelInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, &channelInfo{
			Name:          info.Name,
			Subscribers:   info.Subscribers,
			MessageCount:  info.MessageCount,
			LastMessageTS: info.LastMessageTS,
		})
	}
	return out, nil
}

func (c localChannelClient) ChannelDrain(ctx context.Context, name, instance string, since *int64, wait time.Duration) (*drainResp, error) {
	dr, err := c.store.Drain(ctx, name, instance, since, wait)
	if err != nil {
		return nil, err
	}
	out := &drainResp{Cursor: dr.Cursor}
	for _, msg := range dr.Messages {
		out.Messages = append(out.Messages, &channelMessage{
			Seq:    msg.Seq,
			Sender: msg.Sender,
			Body:   msg.Body,
			TS:     msg.TS,
		})
	}
	return out, nil
}

func (c localChannelClient) ChannelPublish(name, sender, body string) (*publishResp, error) {
	res, err := c.store.Publish(name, sender, body)
	if err != nil {
		return nil, err
	}
	return &publishResp{Seq: res.Seq, TS: res.TS}, nil
}

func (c localChannelClient) ChannelDelete(name string) error {
	removed, err := c.store.Delete(name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no such channel %q", name)
	}
	return nil
}

// humanAge returns a compact human-readable duration ("3m", "2h", "1d").
func humanAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
