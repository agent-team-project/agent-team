package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
)

// channelTestEnv stands up a fresh daemon-side ChannelStore and an httptest
// server backed by daemon.Handler, returning a daemonClient pointed at it.
type channelTestEnv struct {
	client *daemonClient
	srv    *httptest.Server
	store  *daemon.ChannelStore
}

func newChannelTestEnv(t *testing.T) *channelTestEnv {
	t.Helper()
	root := t.TempDir()
	mgr := daemon.NewInstanceManager(root, nil)
	store := daemon.NewChannelStore(root)
	srv := httptest.NewServer(daemon.Handler(mgr, store, nil, ""))
	c := &daemonClient{
		hc:      &http.Client{Timeout: 0},
		baseURL: srv.URL,
		teamDir: root,
	}
	t.Cleanup(srv.Close)
	return &channelTestEnv{client: c, srv: srv, store: store}
}

func TestClient_ChannelPublishSubscribeDrainAck(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client

	sub, err := c.ChannelSubscribe("#room", "alice")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if !sub.Subscribed || sub.Cursor != 0 {
		t.Errorf("first subscribe: %+v", sub)
	}

	for _, body := range []string{"a", "b", "c"} {
		if _, err := c.ChannelPublish("#room", "manager", body); err != nil {
			t.Fatal(err)
		}
	}

	dr, err := c.ChannelDrain(context.Background(), "#room", "alice", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.Messages) != 3 {
		t.Errorf("drain: got %d want 3", len(dr.Messages))
	}
	if dr.Cursor != 3 {
		t.Errorf("cursor: got %d want 3", dr.Cursor)
	}
	if err := c.ChannelAck("#room", "alice", dr.Cursor); err != nil {
		t.Fatal(err)
	}

	dr2, _ := c.ChannelDrain(context.Background(), "#room", "alice", nil, 0)
	if len(dr2.Messages) != 0 {
		t.Errorf("post-ack drain: got %d want 0", len(dr2.Messages))
	}
}

func TestClient_ChannelDrain_LongPollWakesOnPublish(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client
	if _, err := c.ChannelSubscribe("#live", "alice"); err != nil {
		t.Fatal(err)
	}

	type res struct {
		dr  *drainResp
		err error
		dur time.Duration
	}
	done := make(chan res, 1)
	start := time.Now()
	go func() {
		dr, err := c.ChannelDrain(context.Background(), "#live", "alice", nil, 3*time.Second)
		done <- res{dr: dr, err: err, dur: time.Since(start)}
	}()
	time.Sleep(80 * time.Millisecond)
	if _, err := c.ChannelPublish("#live", "manager", "wake"); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("drain err: %v", r.err)
		}
		if r.dur > 2*time.Second {
			t.Errorf("waited too long: %s", r.dur)
		}
		if len(r.dr.Messages) != 1 || r.dr.Messages[0].Body != "wake" {
			t.Errorf("messages: %+v", r.dr.Messages)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("never returned")
	}
}

func TestClient_ChannelDrain_WithSinceOverride(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client
	for _, body := range []string{"a", "b", "c"} {
		c.ChannelPublish("#x", "s", body)
	}
	since := int64(0)
	dr, err := c.ChannelDrain(context.Background(), "#x", "(cli)", &since, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.Messages) != 3 {
		t.Errorf("since=0 drain: got %d want 3", len(dr.Messages))
	}

	since = 1
	dr, err = c.ChannelDrain(context.Background(), "#x", "(cli)", &since, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.Messages) != 2 {
		t.Errorf("since=1 drain: got %d want 2", len(dr.Messages))
	}
}

func TestClient_ChannelList(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client

	c.ChannelPublish("#a", "s", "1")
	c.ChannelPublish("#a", "s", "2")
	c.ChannelSubscribe("#a", "alice")
	c.ChannelPublish("#b", "s", "1")

	infos, err := c.ChannelList()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("infos: got %d", len(infos))
	}
	// Sorted by name → #a then #b.
	if infos[0].Name != "#a" || infos[1].Name != "#b" {
		t.Errorf("order: %+v", infos)
	}
	if infos[0].Subscribers != 1 || infos[0].MessageCount != 2 {
		t.Errorf("#a info: %+v", infos[0])
	}
}

func TestClient_ChannelDelete(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client

	c.ChannelPublish("#gone", "s", "x")
	if err := c.ChannelDelete("#gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := c.ChannelDelete("#gone"); err == nil {
		t.Errorf("delete of missing channel did not error")
	} else if !strings.Contains(err.Error(), "no such channel") {
		t.Errorf("err: %v", err)
	}
}

func TestClient_ChannelUnsubscribe_Idempotent(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client
	c.ChannelSubscribe("#x", "alice")
	r1, err := c.ChannelUnsubscribe("#x", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Unsubscribed {
		t.Errorf("first unsubscribe: %+v", r1)
	}
	r2, _ := c.ChannelUnsubscribe("#x", "alice")
	if r2.Unsubscribed {
		t.Errorf("second unsubscribe: %+v", r2)
	}
}

func TestChannelCommandsUseLocalStoreWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if err := runChannelPublish(out, stderr, teamDir, "#ops", "tester", "offline broadcast"); err != nil {
		t.Fatalf("publish local channel: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "published seq=1") {
		t.Fatalf("publish output = %q, want seq=1", out.String())
	}

	out.Reset()
	if err := runChannelLs(out, stderr, teamDir); err != nil {
		t.Fatalf("list local channels: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "#ops") || !strings.Contains(out.String(), "1") {
		t.Fatalf("list output = %q, want #ops with one message", out.String())
	}

	out.Reset()
	if err := runChannelShow(out, stderr, teamDir, "#ops"); err != nil {
		t.Fatalf("show local channel: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "offline broadcast") || !strings.Contains(out.String(), "messages:      1") {
		t.Fatalf("show output = %q, want local message", out.String())
	}

	out.Reset()
	if err := runChannelRm(out, stderr, teamDir, "#ops"); err != nil {
		t.Fatalf("rm local channel: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "removed #ops") {
		t.Fatalf("rm output = %q, want removal", out.String())
	}

	out.Reset()
	if err := runChannelLs(out, stderr, teamDir); err != nil {
		t.Fatalf("list after rm: %v\nstderr=%s", err, stderr.String())
	}
	if strings.TrimSpace(out.String()) != "(no channels)" {
		t.Fatalf("list after rm = %q, want no channels", out.String())
	}
}

func TestChannelPublishCommandMessageFile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	messageFile := filepath.Join(tmp, "broadcast.txt")
	if err := os.WriteFile(messageFile, []byte("file broadcast\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	publishFile := NewRootCmd()
	fileOut, fileErr := &bytes.Buffer{}, &bytes.Buffer{}
	publishFile.SetOut(fileOut)
	publishFile.SetErr(fileErr)
	publishFile.SetArgs([]string{"channel", "publish", "#ops", "--target", tmp, "--sender", "tester", "--message-file", messageFile})
	if err := publishFile.Execute(); err != nil {
		t.Fatalf("channel publish --message-file: %v\nstderr=%s", err, fileErr.String())
	}
	if !strings.Contains(fileOut.String(), "published seq=1") {
		t.Fatalf("publish file output = %q, want seq=1", fileOut.String())
	}

	oldInput := sendMessageInput
	sendMessageInput = strings.NewReader("stdin broadcast\n")
	defer func() { sendMessageInput = oldInput }()
	publishStdin := NewRootCmd()
	stdinOut, stdinErr := &bytes.Buffer{}, &bytes.Buffer{}
	publishStdin.SetOut(stdinOut)
	publishStdin.SetErr(stdinErr)
	publishStdin.SetArgs([]string{"channel", "publish", "#ops", "--target", tmp, "--message-file", "-"})
	if err := publishStdin.Execute(); err != nil {
		t.Fatalf("channel publish stdin: %v\nstderr=%s", err, stdinErr.String())
	}
	if !strings.Contains(stdinOut.String(), "published seq=2") {
		t.Fatalf("publish stdin output = %q, want seq=2", stdinOut.String())
	}

	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	if err := runChannelShow(showOut, showErr, teamDir, "#ops"); err != nil {
		t.Fatalf("show channel after publishes: %v\nstderr=%s", err, showErr.String())
	}
	if !strings.Contains(showOut.String(), "file broadcast") || !strings.Contains(showOut.String(), "stdin broadcast") {
		t.Fatalf("show output = %q, want both published bodies", showOut.String())
	}

	conflict := NewRootCmd()
	conflictOut, conflictErr := &bytes.Buffer{}, &bytes.Buffer{}
	conflict.SetOut(conflictOut)
	conflict.SetErr(conflictErr)
	conflict.SetArgs([]string{"channel", "publish", "#ops", "body", "--target", tmp, "--message", "flag"})
	if err := conflict.Execute(); err == nil {
		t.Fatal("channel publish conflicting message sources succeeded")
	}
	if !strings.Contains(conflictErr.String(), "only one of positional args, --message, or --message-file") {
		t.Fatalf("conflict stderr = %q", conflictErr.String())
	}
}

func TestChannelCommandsRoundTripHashNameThroughDaemon(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agt-channel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	run := func(args ...string) (string, string, error) {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), stderr.String(), err
	}

	out, stderr, err := run("channel", "publish", "--target", tmp, "#standup", "Codex docs validation")
	if err != nil {
		t.Fatalf("publish daemon channel: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(out, "published seq=1") {
		t.Fatalf("publish output = %q, want seq=1", out)
	}

	out, stderr, err = run("channels", "--target", tmp)
	if err != nil {
		t.Fatalf("channels: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(out, "#standup") || !strings.Contains(out, "1") {
		t.Fatalf("channels output = %q, want #standup with one message", out)
	}

	out, stderr, err = run("channel", "show", "--target", tmp, "#standup")
	if err != nil {
		t.Fatalf("show daemon channel: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(out, "channel:       #standup") || !strings.Contains(out, "Codex docs validation") {
		t.Fatalf("show output = %q, want #standup message", out)
	}

	out, stderr, err = run("channel", "rm", "--target", tmp, "--force", "#standup")
	if err != nil {
		t.Fatalf("rm daemon channel: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(out, "removed #standup") {
		t.Fatalf("rm output = %q, want #standup removal", out)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{15 * time.Second, "15s"},
		{3 * time.Minute, "3m"},
		{2 * time.Hour, "2h"},
		{36 * time.Hour, "1d"},
	}
	for _, c := range cases {
		if got := humanAge(c.d); got != c.want {
			t.Errorf("humanAge(%s) = %q want %q", c.d, got, c.want)
		}
	}
}
