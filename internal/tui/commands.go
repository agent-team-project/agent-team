package tui

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

type commandRuntime struct {
	ctx     context.Context
	teamDir string
	build   buildinfo.Info
	clock   func() time.Time
}

type refreshBatch struct {
	messages []Msg
}

func (runtime *commandRuntime) load(includeCache bool) refreshBatch {
	at := runtime.now()
	messages := []Msg{}
	cacheUsed := false
	if includeCache {
		if cached, err := daemonclient.LoadSnapshotCache(runtime.teamDir); err == nil {
			messages = append(messages, CachedSnapshot{Snapshot: cached})
			cacheUsed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			// Invalid cache is deliberately ignored; discovery still proceeds and
			// the honest live result determines the connection state.
			cacheUsed = false
		}
	}
	client, err := daemonclient.New(runtime.teamDir, daemonclient.Options{
		Timeout:   4 * time.Second,
		Build:     runtime.build,
		KeepAlive: true,
	})
	if err != nil {
		messages = append(messages, RefreshFinished{At: at, CacheUsed: cacheUsed, Error: err.Error()})
		return refreshBatch{messages: messages}
	}
	defer client.CloseIdleConnections()
	snapshot := client.Snapshot(runtime.ctx, at)
	anySuccess := false
	for _, source := range daemonclient.SnapshotSources() {
		if fetchedAt := snapshot.SourceTimes[source]; !fetchedAt.IsZero() {
			messages = append(messages, SnapshotOK{Source: source, Snapshot: snapshot, At: fetchedAt})
			anySuccess = true
		} else if source == daemonclient.SourceResources && len(snapshot.Resources) > 0 {
			messages = append(messages, SnapshotOK{Source: source, Snapshot: snapshot, At: at})
		}
		if message := snapshot.SourceErrors[source]; message != "" {
			messages = append(messages, SnapshotError{Source: source, Error: message, At: at})
		}
	}
	if snapshot.Complete() {
		_ = daemonclient.SaveSnapshotCache(runtime.teamDir, snapshot)
	}
	messages = append(messages, RefreshFinished{
		At: at, AnySuccess: anySuccess, Complete: snapshot.Complete(), CacheUsed: cacheUsed,
	})
	return refreshBatch{messages: messages}
}

func (runtime *commandRuntime) now() time.Time {
	if runtime.clock == nil {
		return time.Now().UTC()
	}
	return runtime.clock().UTC()
}
